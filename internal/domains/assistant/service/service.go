// Package service implements the assistant use cases: the fixed AI pipeline
// for one member message, reply ratings and the retention purge.
//
// Pipeline order (docs/requirements/mvp.md § 5; ADR-0017, ADR-0023; the
// crisis-path skill). Do not reorder:
//
//  1. L1: the local crisis lexicon on the raw text, before everything else.
//     A hit never reaches the model: escalation and the fixed crisis
//     response, whatever the consent or the limit (non-negotiable 1);
//  2. ai_crossborder consent: without it (403, or for a crisis the crisis
//     response) nothing is stored; then the idempotent replay of a repeated
//     Idempotency-Key;
//  3. daily limit (429), never for a crisis. A crisis with consent stores
//     the anonymized text with the crisis flag;
//  4. anonymization (package anonymize) with the member's display name and
//     the name dictionary;
//  5. retrieval (failure is not fatal: no fragments, the model must say it
//     does not know);
//  6. the model, with the versioned system prompt, fragments and the last
//     anonymized turns; its JSON is parsed strictly;
//  7. L2: crisis=true from the model is handled exactly like L1 and no model
//     text is returned;
//  8. any provider failure (timeout, error, rate limit, refusal, invalid
//     JSON) gives degraded=true, no text, suggest none; never a 5xx;
//  9. persistence of anonymized, sealed text only, with
//     assistant.QuestionAnswered in the same transaction (see persist).
//
// assistant.QuestionAnswered (ADR-0024 addendum 2026-10-28) is emitted
// exactly once per stored exchange: only by the transaction whose
// InsertExchange actually inserted it. A replay of a known Idempotency-Key,
// or a concurrent request that lost the insert race, emits nothing. A crisis
// without the ai_crossborder consent stores nothing, so it emits nothing
// either (its escalation is counted by safety.EscalationOpened). There is no
// streaming: one request is one finished turn.
//
// Logs carry IDs, event names, outcome, latency and model ID; never message
// text, anonymized text or a member ID next to a crisis flag.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/anonymize"
	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/port"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
)

// Defaults for Settings fields left zero.
const (
	DefaultDailyLimit          = 50
	DefaultTimeZone            = "Asia/Almaty"
	DefaultLang                = model.LangRU
	DefaultHistoryTurns        = 10
	DefaultRetrievalK          = 5
	DefaultRightsDisclaimerKey = "assistant.disclaimer.lawyer"
	DefaultRetentionDays       = 90
	DefaultPurgeBatchSize      = 1000
	maxInputChars              = 2000
)

// Settings holds the assistant runtime configuration.
type Settings struct {
	// DailyLimit is the number of member messages per calendar day (§ 4.3; default 50).
	DailyLimit int
	// TimeZone is the deployment IANA zone that defines "day" for the limit
	// (default Asia/Almaty). Hosts need current tzdata.
	TimeZone string
	// DefaultLang is the reply language when neither the request nor the
	// profile has one (default ru).
	DefaultLang string
	// CrisisPhones are returned with every crisis response (from configuration, never code).
	CrisisPhones []model.CrisisPhone
	// NameDictionary is the list of common names the anonymizer replaces
	// (data from configuration; tests use anonymize/testdata/names.txt).
	NameDictionary []string
	// Prompt is the versioned system prompt (LoadPrompt). Required.
	Prompt Prompt
	// HistoryTurns is how many earlier turns are sent to the model (default 10).
	HistoryTurns int
	// RetrievalK is the number of knowledge fragments requested (default 5).
	RetrievalK int
	// RightsDisclaimerKey is the locale key of the "does not replace a
	// lawyer" phrase added to rights answers (§ 4.7).
	RightsDisclaimerKey string
	// RetentionDays is how long turns are kept (default 90, § 7).
	RetentionDays int
	// PurgeBatchSize bounds one purge transaction (default 1000).
	PurgeBatchSize int
}

// Prompt is a versioned system prompt. Version names the file
// (assistant.v1) and goes into logs and evaluation reports (ADR-0017).
type Prompt struct {
	Version string
	Text    string
}

// LoadPrompt reads the system prompt from path (configuration). The version
// is the file name without its extension.
func LoadPrompt(path string) (Prompt, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: path comes from operator configuration
	if err != nil {
		return Prompt{}, fmt.Errorf("assistant: read prompt: %w", err)
	}
	text := strings.TrimSpace(string(b))
	if text == "" || !utf8.ValidString(text) {
		return Prompt{}, fmt.Errorf("assistant: prompt %s is empty or not UTF-8", filepath.Base(path))
	}
	base := filepath.Base(path)
	return Prompt{Version: strings.TrimSuffix(base, filepath.Ext(base)), Text: text}, nil
}

// MessageInput is one member message.
type MessageInput struct {
	Text  string
	Lang  string // optional: kk, ru or en
	Scope string // general or rights
}

// Service is the assistant application service.
type Service struct {
	Store       port.Store
	Sealer      port.Sealer
	Consent     port.ConsentChecker
	Crisis      port.CrisisDetector
	Escalations port.CrisisOpener
	Profiles    port.Profiles
	Retriever   port.Retriever
	LLM         port.LLM
	Clock       clock.Clock
	Logger      *slog.Logger
	Settings    Settings

	anonOnce sync.Once
	anon     *anonymize.Anonymizer
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) anonymizer() *anonymize.Anonymizer {
	s.anonOnce.Do(func() { s.anon = anonymize.New(s.Settings.NameDictionary) })
	return s.anon
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func (s *Service) dayStart(now time.Time) (time.Time, error) {
	name := s.Settings.TimeZone
	if name == "" {
		name = DefaultTimeZone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.Time{}, fmt.Errorf("assistant: load time zone %q: %w", name, err)
	}
	l := now.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc).UTC(), nil
}

// SendMessage runs the pipeline for one member message (see the package
// documentation for the order). Errors: model.ErrUnauthorized,
// model.ErrInvalidInput, model.ErrConsentRequired, model.ErrDailyLimit, or
// an infrastructure error (database, consent store). Provider failures are
// never errors: they return a degraded reply.
func (s *Service) SendMessage(ctx context.Context, memberID, idempotencyKey uuid.UUID, in MessageInput) (model.Reply, error) {
	if memberID == uuid.Nil {
		return model.Reply{}, model.ErrUnauthorized
	}
	if idempotencyKey == uuid.Nil {
		return model.Reply{}, fmt.Errorf("%w: idempotency key", model.ErrInvalidInput)
	}
	if strings.TrimSpace(in.Text) == "" || !utf8.ValidString(in.Text) || utf8.RuneCountInString(in.Text) > maxInputChars {
		return model.Reply{}, fmt.Errorf("%w: text", model.ErrInvalidInput)
	}
	if !model.ScopeIsValid(in.Scope) {
		return model.Reply{}, fmt.Errorf("%w: scope", model.ErrInvalidInput)
	}
	if in.Lang != "" && !model.LangIsValid(in.Lang) {
		return model.Reply{}, fmt.Errorf("%w: lang", model.ErrInvalidInput)
	}
	if s.Crisis == nil {
		// Fail closed: without the level-1 detector no text goes anywhere.
		return model.Reply{}, errors.New("assistant: crisis detector not configured")
	}

	// 1. L1 first, on the raw text: it is local and never crosses the border,
	// so a crisis gets a person whatever the consent or the limit.
	l1 := s.Crisis.Detect(in.Text, in.Lang)

	// 2. Consent. Without it nothing is stored, retrieved or sent; a crisis
	// still gets the escalation and the crisis response.
	if s.Consent == nil {
		if l1 {
			return s.crisisWithoutStorage(ctx, memberID), nil
		}
		return model.Reply{}, errors.New("assistant: consent checker not configured")
	}
	ok, err := s.Consent.HasConsent(ctx, memberID, model.ConsentAICrossborder)
	if err != nil || !ok {
		if l1 {
			if err != nil {
				s.log().Warn("assistant consent unavailable", "event", "AssistantConsentUnavailable", "error", err.Error())
			}
			return s.crisisWithoutStorage(ctx, memberID), nil
		}
		if err != nil {
			return model.Reply{}, fmt.Errorf("assistant: consent: %w", err)
		}
		return model.Reply{}, model.ErrConsentRequired
	}

	now := s.Clock.Now().UTC()
	since, err := s.dayStart(now)
	if err != nil {
		return model.Reply{}, err
	}
	limit := orDefault(s.Settings.DailyLimit, DefaultDailyLimit)

	// Replay: a repeated key has one effect (no second model call, no second count).
	var (
		replayed bool
		reply    model.Reply
		count    int
	)
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		mt, at, err := tx.GetExchangeByIdempotencyKey(ctx, memberID, idempotencyKey)
		switch {
		case err == nil:
			replayed = true
			reply, err = s.replay(ctx, mt, at)
			if err != nil {
				return err
			}
		case !errors.Is(err, model.ErrNotFound):
			return err
		}
		count, err = tx.CountMemberTurnsSince(ctx, memberID, since)
		return err
	})
	if err != nil {
		return model.Reply{}, err
	}
	if replayed {
		reply.RemainingToday = max(limit-count, 0)
		return reply, nil
	}

	// 3. Daily limit; never for a crisis.
	if count >= limit && !l1 {
		return model.Reply{}, model.ErrDailyLimit
	}
	remaining := max(limit-count-1, 0)

	ex := exchange{memberID: memberID, key: idempotencyKey, scope: in.Scope, now: now, remaining: remaining}
	if ex.memberTurnID, err = uuid.NewV7(); err != nil {
		return model.Reply{}, fmt.Errorf("assistant: message id: %w", err)
	}
	if ex.assistantTurnID, err = uuid.NewV7(); err != nil {
		return model.Reply{}, fmt.Errorf("assistant: message id: %w", err)
	}

	if l1 {
		// The escalation is opened before the profile lookup, so nothing but
		// our own database stands between the message and a person.
		return s.crisisReply(ctx, ex, "l1", in.Text)
	}

	// 4. Anonymize. The profile is needed for the member's own name; when it
	// is unavailable the model is not asked (her name could leak).
	profile, profileErr := s.profile(ctx, memberID)
	ex.ownName = profile.DisplayName
	ex.memberText = s.anonymizer().Anonymize(in.Text, profile.DisplayName)
	if profileErr != nil {
		s.log().Warn("assistant profile unavailable", "event", "AssistantProfileUnavailable", "message_id", ex.assistantTurnID, "error", profileErr.Error())
		return s.degradedReply(ctx, ex, model.FailureUnavailable, "", 0)
	}

	lang := in.Lang
	if lang == "" {
		lang = profile.Lang
	}
	if !model.LangIsValid(lang) {
		lang = s.Settings.DefaultLang
		if !model.LangIsValid(lang) {
			lang = DefaultLang
		}
	}

	// 5. Retrieval; failure leaves the model without sources.
	fragments, retrievalFailed := s.retrieve(ctx, ex.assistantTurnID, lang, in.Scope, ex.memberText)

	// 6. The model.
	if s.LLM == nil {
		return s.degradedReply(ctx, ex, model.FailureUnavailable, "", 0)
	}
	if s.Settings.Prompt.Text == "" {
		return model.Reply{}, errors.New("assistant: system prompt not configured")
	}
	history := s.history(ctx, memberID, ex.assistantTurnID)
	started := s.Clock.Now()
	comp, err := s.LLM.Complete(ctx, model.LLMRequest{
		SystemPrompt: s.Settings.Prompt.Text,
		Lang:         lang,
		Scope:        in.Scope,
		History:      history,
		Fragments:    fragments,
		Text:         ex.memberText,
	})
	latency := s.Clock.Now().Sub(started)
	if err != nil {
		kind := model.FailureAPIError
		var pe *model.ProviderError
		if errors.As(err, &pe) {
			kind = pe.Kind
		}
		return s.degradedReply(ctx, ex, kind, comp.ModelID, latency)
	}
	out, err := model.ParseOutput(comp.JSON)
	if err != nil {
		return s.degradedReply(ctx, ex, model.FailureInvalidOutput, comp.ModelID, latency)
	}

	// 7. L2: the model's crisis flag wins over everything else in its output.
	if out.Crisis {
		return s.crisisReply(ctx, ex, "l2", "")
	}
	kept, err := model.Validate(out, fragments)
	if err != nil {
		return s.degradedReply(ctx, ex, model.FailureInvalidOutput, comp.ModelID, latency)
	}

	// 9. Persist. The reply the member sees is the model text; the stored copy
	// is anonymized again because the model may echo a name or number from a
	// fragment.
	sources := make([]model.SourceRef, 0, len(kept))
	for _, f := range kept {
		sources = append(sources, model.SourceRef{ArticleID: f.ArticleID, Title: f.Title, SourceURL: f.SourceURL})
	}
	text := out.Reply
	reply = model.Reply{
		MessageID:      ex.assistantTurnID,
		Text:           &text,
		Sources:        sources,
		Suggest:        out.Suggest,
		RemainingToday: remaining,
	}
	if in.Scope == model.ScopeRights {
		reply.DisclaimerKey = s.Settings.RightsDisclaimerKey
		if reply.DisclaimerKey == "" {
			reply.DisclaimerKey = DefaultRightsDisclaimerKey
		}
	}
	// The answer source follows what the pipeline did: retrieval down means
	// the knowledge base was unavailable to the model (logged as
	// AssistantRetrievalFailed); otherwise a reply backed by at least one
	// retrieved, reviewed article is "knowledge" and anything else "no_answer".
	source := model.SourceNoAnswer
	switch {
	case retrievalFailed:
		source = model.SourceUnavailable
	case len(sources) > 0:
		source = model.SourceKnowledge
	}
	stored := s.anonymizer().Anonymize(out.Reply, ex.ownName)
	final, err := s.persist(ctx, ex, model.Turn{Sources: sources, Suggest: out.Suggest}, &stored, reply, source)
	if err != nil {
		return model.Reply{}, err
	}
	s.log().Info("assistant replied", "event", "AssistantReplied", "message_id", final.MessageID,
		"outcome", "ok", "source", source, "latency_ms", latency.Milliseconds(), "model", comp.ModelID,
		"prompt_version", s.Settings.Prompt.Version, "sources", len(sources))
	return final, nil
}

// exchange carries one message through the pipeline.
type exchange struct {
	memberID        uuid.UUID
	key             uuid.UUID
	scope           string
	now             time.Time
	memberTurnID    uuid.UUID
	assistantTurnID uuid.UUID
	memberText      string // anonymized
	ownName         string
	remaining       int
}

func (s *Service) profile(ctx context.Context, memberID uuid.UUID) (model.Profile, error) {
	if s.Profiles == nil {
		return model.Profile{}, errors.New("assistant: profiles not configured")
	}
	return s.Profiles.Profile(ctx, memberID)
}

// retrieve returns the fragments and whether retrieval failed with an error.
// A missing or disabled retriever (NoRetrieval) is not a failure: it finds
// nothing, which is a "no_answer", not an outage.
func (s *Service) retrieve(ctx context.Context, messageID uuid.UUID, lang, scope, text string) (frags []model.Fragment, failed bool) {
	if s.Retriever == nil {
		return nil, false
	}
	k := orDefault(s.Settings.RetrievalK, DefaultRetrievalK)
	frags, err := s.Retriever.Search(ctx, lang, scope, text, k)
	if err != nil {
		s.log().Warn("assistant retrieval failed", "event", "AssistantRetrievalFailed", "message_id", messageID, "error", err.Error())
		return nil, true
	}
	if len(frags) > k {
		frags = frags[:k]
	}
	return frags, false
}

// history returns up to HistoryTurns earlier turns, oldest first, starting
// with a member turn. Turns without text (crisis, degraded) and turns that
// cannot be opened are skipped; history is best effort.
func (s *Service) history(ctx context.Context, memberID, messageID uuid.UUID) []model.HistoryTurn {
	n := orDefault(s.Settings.HistoryTurns, DefaultHistoryTurns)
	var turns []model.Turn
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		turns, err = tx.ListRecentTurns(ctx, memberID, n)
		return err
	})
	if err != nil {
		s.log().Warn("assistant history unavailable", "event", "AssistantHistoryUnavailable", "message_id", messageID, "error", err.Error())
		return nil
	}
	out := make([]model.HistoryTurn, 0, len(turns))
	failed := 0
	for i := len(turns) - 1; i >= 0; i-- {
		t := turns[i]
		if t.SealedText == nil || t.Crisis {
			continue
		}
		plain, err := s.open(ctx, t)
		if err != nil {
			failed++
			continue
		}
		if len(out) == 0 && t.Role != model.RoleMember {
			continue
		}
		out = append(out, model.HistoryTurn{Role: t.Role, Text: plain})
	}
	if failed > 0 {
		s.log().Warn("assistant history open failed", "event", "AssistantHistoryOpenFailed", "message_id", messageID, "turns", failed)
	}
	return out
}

func (s *Service) seal(ctx context.Context, memberID, turnID uuid.UUID, text string) ([]byte, error) {
	if s.Sealer == nil {
		return nil, errors.New("assistant: sealer not configured")
	}
	return s.Sealer.Seal(ctx, memberID, []byte(text), crypto.AssociatedData(model.TextField, turnID))
}

func (s *Service) open(ctx context.Context, t model.Turn) (string, error) {
	if s.Sealer == nil {
		return "", errors.New("assistant: sealer not configured")
	}
	b, err := s.Sealer.Open(ctx, t.MemberID, t.SealedText, crypto.AssociatedData(model.TextField, t.ID))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// crisisWithoutStorage answers a crisis from a member without the
// ai_crossborder consent (or when it cannot be checked): escalation and the
// fixed crisis response, and nothing stored, so the message ID is not
// persisted and cannot be rated. RemainingToday is unknown (-1).
func (s *Service) crisisWithoutStorage(ctx context.Context, memberID uuid.UUID) model.Reply {
	id, err := uuid.NewV7()
	if err != nil {
		id = uuid.New()
	}
	reply := model.Reply{
		MessageID:      id,
		Crisis:         true,
		CrisisPhones:   append([]model.CrisisPhone(nil), s.Settings.CrisisPhones...),
		Sources:        []model.SourceRef{},
		Suggest:        model.SuggestNone,
		RemainingToday: -1,
	}
	reply.EscalationID = s.escalate(ctx, memberID, id)
	s.log().Info("assistant crisis", "event", "AssistantCrisis", "message_id", id, "level", "l1", "stored", false)
	return reply
}

// escalate opens the escalation; a failure is logged for alerting and the
// crisis response goes out anyway.
func (s *Service) escalate(ctx context.Context, memberID, messageID uuid.UUID) *uuid.UUID {
	if s.Escalations == nil {
		s.log().Error("crisis escalation failed", "event", "CrisisEscalationFailed", "message_id", messageID, "error", "crisis opener not configured")
		return nil
	}
	escID, err := s.Escalations.OpenEscalation(ctx, memberID, model.EscalationSourceAssistant)
	if err != nil {
		s.log().Error("crisis escalation failed", "event", "CrisisEscalationFailed", "message_id", messageID, "error", err.Error())
		return nil
	}
	return &escID
}

// crisisReply is steps 3 and 7: escalation to a person, the fixed crisis
// response (the client shows the locale text and these phones) and the
// stored anonymized member text with the crisis flag. It never depends on
// the model, and a failed escalation or a failed write still returns the
// phones (non-negotiable 1).
//
// rawText is set on the L1 path, where the text is anonymized only after the
// escalation is open; on the L2 path ex already holds the anonymized text.
func (s *Service) crisisReply(ctx context.Context, ex exchange, level, rawText string) (model.Reply, error) {
	reply := model.Reply{
		MessageID:      ex.assistantTurnID,
		Crisis:         true,
		CrisisPhones:   append([]model.CrisisPhone(nil), s.Settings.CrisisPhones...),
		Sources:        []model.SourceRef{},
		Suggest:        model.SuggestNone,
		RemainingToday: ex.remaining,
	}
	reply.EscalationID = s.escalate(ctx, ex.memberID, ex.assistantTurnID)
	if rawText != "" {
		profile, err := s.profile(ctx, ex.memberID)
		if err != nil {
			// Stored in-jurisdiction and sealed; only the dictionary applies.
			s.log().Warn("assistant profile unavailable", "event", "AssistantProfileUnavailable", "message_id", ex.assistantTurnID, "error", err.Error())
		}
		ex.memberText = s.anonymizer().Anonymize(rawText, profile.DisplayName)
	}
	final, err := s.persist(ctx, ex, model.Turn{Crisis: true, Suggest: model.SuggestNone, EscalationID: reply.EscalationID}, nil, reply, model.SourceCrisis)
	if err != nil {
		s.log().Error("assistant crisis turn not stored", "event", "AssistantCrisisPersistFailed", "message_id", ex.assistantTurnID, "error", err.Error())
		final = reply
	}
	s.log().Info("assistant crisis", "event", "AssistantCrisis", "message_id", final.MessageID, "level", level)
	return final, nil
}

// degradedReply is step 8: the provider failed, so the client shows a polite
// message, the menu and "Contact a person". Never an error.
func (s *Service) degradedReply(ctx context.Context, ex exchange, kind, modelID string, latency time.Duration) (model.Reply, error) {
	reply := model.Reply{
		MessageID:      ex.assistantTurnID,
		Degraded:       true,
		Sources:        []model.SourceRef{},
		Suggest:        model.SuggestNone,
		RemainingToday: ex.remaining,
	}
	final, err := s.persist(ctx, ex, model.Turn{Degraded: true, Suggest: model.SuggestNone}, nil, reply, model.SourceUnavailable)
	if err != nil {
		return model.Reply{}, err
	}
	s.log().Warn("assistant degraded", "event", "AssistantDegraded", "message_id", final.MessageID,
		"kind", kind, "latency_ms", latency.Milliseconds(), "model", modelID, "prompt_version", s.Settings.Prompt.Version)
	return final, nil
}

// persist seals and stores the member turn and the assistant turn in one
// transaction, together with assistant.QuestionAnswered (source). When a
// concurrent request with the same idempotency key won, the stored exchange
// is returned instead of reply and no event is emitted: the winner emitted it.
//
// Crisis turns (at.Crisis) treat the event as best effort, so the crisis
// response never depends on it (non-negotiable 1):
//   - by the time persist runs, crisisReply has already opened the escalation
//     and built the reply with the phones; nothing here can change them;
//   - the event is one INSERT into the local outbox inside the transaction
//     the crisis path already waits on for its turn. No broker, no network:
//     the relay publishes later;
//   - PublishQuestionAnswered runs in its own savepoint. If it fails, the
//     error is not returned from the transaction, so the turn still commits
//     and the event is dropped with an Error log (the metric loses one count);
//   - if the whole transaction fails anyway, crisisReply logs it and returns
//     the crisis reply.
//
// For every other turn an event failure fails the transaction, so a stored
// turn always has exactly one event.
func (s *Service) persist(ctx context.Context, ex exchange, at model.Turn, assistantText *string, reply model.Reply, source string) (model.Reply, error) {
	memberSealed, err := s.seal(ctx, ex.memberID, ex.memberTurnID, ex.memberText)
	if err != nil {
		return model.Reply{}, fmt.Errorf("assistant: seal member turn: %w", err)
	}
	var assistantSealed []byte
	if assistantText != nil {
		if assistantSealed, err = s.seal(ctx, ex.memberID, ex.assistantTurnID, *assistantText); err != nil {
			return model.Reply{}, fmt.Errorf("assistant: seal assistant turn: %w", err)
		}
	}
	key := ex.key
	replyTo := ex.memberTurnID
	mt := model.Turn{
		ID: ex.memberTurnID, MemberID: ex.memberID, Role: model.RoleMember,
		SealedText: memberSealed, Crisis: at.Crisis, Suggest: model.SuggestNone,
		IdempotencyKey: &key, CreatedAt: ex.now,
	}
	at.ID, at.MemberID, at.Role, at.ReplyTo = ex.assistantTurnID, ex.memberID, model.RoleAssistant, &replyTo
	at.SealedText, at.CreatedAt = assistantSealed, ex.now
	if at.Sources == nil {
		at.Sources = []model.SourceRef{}
	}

	out := reply
	var droppedEvent error
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		droppedEvent = nil
		inserted, err := tx.InsertExchange(ctx, mt, at)
		if err != nil {
			return err
		}
		if inserted {
			evErr := tx.PublishQuestionAnswered(ctx, ex.now, model.QuestionAnsweredData{
				MessageID: ex.assistantTurnID, Scope: ex.scope, Source: source,
			})
			if evErr != nil && at.Crisis {
				droppedEvent = evErr
				return nil
			}
			return evErr
		}
		existingMember, existingAssistant, err := tx.GetExchangeByIdempotencyKey(ctx, ex.memberID, ex.key)
		if err != nil {
			return err
		}
		out, err = s.replay(ctx, existingMember, existingAssistant)
		out.RemainingToday = ex.remaining
		return err
	})
	if err != nil {
		return model.Reply{}, err
	}
	if droppedEvent != nil {
		s.log().Error("assistant event dropped", "event", "AssistantQuestionAnsweredDropped",
			"message_id", ex.assistantTurnID, "error", droppedEvent.Error())
	}
	return out, nil
}

// replay rebuilds the reply of a stored exchange. The text is the stored
// anonymized copy: the original model text is never kept.
func (s *Service) replay(ctx context.Context, _ model.Turn, at model.Turn) (model.Reply, error) {
	r := model.Reply{
		MessageID:    at.ID,
		Crisis:       at.Crisis,
		Degraded:     at.Degraded,
		Sources:      at.Sources,
		Suggest:      at.Suggest,
		EscalationID: at.EscalationID,
	}
	if r.Sources == nil {
		r.Sources = []model.SourceRef{}
	}
	if !model.SuggestIsValid(r.Suggest) {
		r.Suggest = model.SuggestNone
	}
	if at.Crisis {
		r.CrisisPhones = append([]model.CrisisPhone(nil), s.Settings.CrisisPhones...)
		return r, nil
	}
	if at.SealedText != nil {
		text, err := s.open(ctx, at)
		if err != nil {
			return model.Reply{}, fmt.Errorf("assistant: open stored reply: %w", err)
		}
		r.Text = &text
	}
	return r, nil
}

// RateReply records "Helped / Did not help" for the member's own assistant
// turn (the last rating wins) and emits assistant.ReplyRated through the
// outbox in the same transaction when the rating changes.
func (s *Service) RateReply(ctx context.Context, memberID, messageID uuid.UUID, helped bool) error {
	if memberID == uuid.Nil {
		return model.ErrUnauthorized
	}
	if messageID == uuid.Nil {
		return model.ErrNotFound
	}
	now := s.Clock.Now().UTC()
	changed := false
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		current, err := tx.GetRatingForUpdate(ctx, memberID, messageID)
		if err != nil {
			return err
		}
		if current != nil && *current == helped {
			return nil
		}
		if err := tx.SetRating(ctx, memberID, messageID, helped, now); err != nil {
			return err
		}
		changed = true
		return tx.PublishReplyRated(ctx, now, model.ReplyRatedData{MessageID: messageID, Helped: helped})
	})
	if err != nil {
		return err
	}
	if changed {
		s.log().Info("assistant reply rated", "event", model.EventReplyRated, "message_id", messageID)
	}
	return nil
}
