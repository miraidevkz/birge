// Package model holds the assistant domain types, the model-output contract
// and its validation. No I/O.
package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Turn roles stored in assistant.messages.
const (
	RoleMember    = "member"
	RoleAssistant = "assistant"
)

// Retrieval scopes (contract AssistantMessageInput.scope).
const (
	ScopeGeneral = "general"
	ScopeRights  = "rights"
)

// Languages of the MVP.
const (
	LangKK = "kk"
	LangRU = "ru"
	LangEN = "en"
)

// Suggestions the model may return (contract AssistantSuggestion).
const (
	SuggestNone         = "none"
	SuggestTea          = "tea"
	SuggestPsychologist = "psychologist"
	SuggestRights       = "rights"
	SuggestPractice     = "practice"
)

// ConsentAICrossborder is the privacy consent type required before any text
// leaves the jurisdiction (docs/requirements/mvp.md N2; ADR-0013).
const ConsentAICrossborder = "ai_crossborder"

// EscalationSourceAssistant is passed to the crisis opener (safety source enum).
const EscalationSourceAssistant = "assistant"

// TextField is the associated-data field name for sealed turn text (ADR-0009).
const TextField = "assistant.messages.text"

// TimerRetentionPurge is the daily platform timer that deletes turns older
// than the retention period (ADR-0020).
const TimerRetentionPurge = "assistant.RetentionPurge"

// RetentionPurgeSubject is the fixed subject of the singleton purge timer;
// platform timers require a non-nil subject. The value is arbitrary but must
// never change, or two purge timers could be active after an upgrade.
var RetentionPurgeSubject = uuid.MustParse("019a2f00-0000-7000-8000-000000000700")

// Domain events (contracts/events).
const (
	EventReplyRated        = "assistant.ReplyRated"
	EventReplyRatedVersion = 1
)

// Limits of the model-output contract (contract AssistantReply).
const (
	MaxReplyChars = 2000
	MaxSources    = 5
)

// Provider failure kinds, logged as kind=<value> (no text).
const (
	FailureTimeout       = "timeout"
	FailureRateLimited   = "rate_limited"
	FailureAPIError      = "api_error"
	FailureRefusal       = "refusal"
	FailureTruncated     = "truncated"
	FailureInvalidOutput = "invalid_output"
	FailureUnavailable   = "unavailable"
)

var (
	// ErrUnauthorized means the caller has no member session.
	ErrUnauthorized = errors.New("assistant: unauthorized")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("assistant: invalid input")
	// ErrConsentRequired means the member has not granted ai_crossborder.
	ErrConsentRequired = errors.New("assistant: consent required")
	// ErrDailyLimit means the member reached the configured daily message limit.
	ErrDailyLimit = errors.New("assistant: daily limit reached")
	// ErrNotFound means the message does not exist or belongs to someone else.
	ErrNotFound = errors.New("assistant: not found")
	// ErrInvalidOutput means the model output broke the JSON contract.
	ErrInvalidOutput = errors.New("assistant: invalid model output")
)

// ProviderError is an LLM failure. The service turns every provider error into
// a degraded reply; Kind is for logs and metrics only.
type ProviderError struct {
	Kind string
	Err  error
}

// Error implements error without any message text.
func (e *ProviderError) Error() string {
	if e.Err == nil {
		return "assistant: provider " + e.Kind
	}
	return "assistant: provider " + e.Kind + ": " + e.Err.Error()
}

// Unwrap returns the cause.
func (e *ProviderError) Unwrap() error { return e.Err }

// LangIsValid reports whether l is a supported language.
func LangIsValid(l string) bool { return l == LangKK || l == LangRU || l == LangEN }

// ScopeIsValid reports whether s is a retrieval scope.
func ScopeIsValid(s string) bool { return s == ScopeGeneral || s == ScopeRights }

// SuggestIsValid reports whether s is in the suggestion enum.
func SuggestIsValid(s string) bool {
	switch s {
	case SuggestNone, SuggestTea, SuggestPsychologist, SuggestRights, SuggestPractice:
		return true
	}
	return false
}

// CrisisPhone is a crisis line from configuration (never from code).
type CrisisPhone struct {
	Number string
	Label  string
}

// Profile is what the assistant needs from the member's profile: the display
// name (to anonymize it) and the interface language (default reply language).
type Profile struct {
	DisplayName string
	Lang        string
}

// Fragment is one knowledge-base passage returned by retrieval. Its text is
// untrusted data for the model, never instructions.
type Fragment struct {
	ArticleID uuid.UUID
	Title     string
	SourceURL string
	Text      string
}

// SourceRef is a knowledge-base reference stored with an assistant turn.
type SourceRef struct {
	ArticleID uuid.UUID `json:"article_id"`
	Title     string    `json:"title"`
	SourceURL string    `json:"source_url,omitempty"`
}

// HistoryTurn is one earlier anonymized turn sent to the model.
type HistoryTurn struct {
	Role string
	Text string
}

// LLMRequest is everything the model sees. Every text in it is anonymized;
// fragments and member text are data, framed as such by the adapter.
type LLMRequest struct {
	SystemPrompt string
	Lang         string
	Scope        string
	History      []HistoryTurn
	Fragments    []Fragment
	Text         string
}

// Completion is the raw model output: the JSON text and the model that produced it.
type Completion struct {
	JSON    string
	ModelID string
}

// Output is the parsed model output {reply, crisis, sources, suggest}.
type Output struct {
	Reply   string   `json:"reply"`
	Crisis  bool     `json:"crisis"`
	Sources []string `json:"sources"`
	Suggest string   `json:"suggest"`
}

type rawOutput struct {
	Reply   *string   `json:"reply"`
	Crisis  *bool     `json:"crisis"`
	Sources *[]string `json:"sources"`
	Suggest *string   `json:"suggest"`
}

// ParseOutput decodes the model JSON strictly: one object, all four fields
// present with the right types, no unknown fields, nothing after it. It does
// not check the reply length or the enum: a crisis flag must be honoured even
// when the rest of the output is unusable (see Validate).
func ParseOutput(raw string) (Output, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var r rawOutput
	if err := dec.Decode(&r); err != nil {
		return Output{}, fmt.Errorf("%w: decode: %w", ErrInvalidOutput, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Output{}, fmt.Errorf("%w: trailing data", ErrInvalidOutput)
	}
	if r.Reply == nil || r.Crisis == nil || r.Sources == nil || r.Suggest == nil {
		return Output{}, fmt.Errorf("%w: missing field", ErrInvalidOutput)
	}
	return Output{Reply: *r.Reply, Crisis: *r.Crisis, Sources: *r.Sources, Suggest: *r.Suggest}, nil
}

// Validate checks a non-crisis output against the contract and returns the
// sources that are among the retrieved fragments, in model order, without
// duplicates, at most MaxSources. Sources the model invented are dropped,
// never shown: a member must not follow a link we did not retrieve.
func Validate(out Output, retrieved []Fragment) ([]Fragment, error) {
	if !utf8.ValidString(out.Reply) {
		return nil, fmt.Errorf("%w: reply encoding", ErrInvalidOutput)
	}
	n := utf8.RuneCountInString(out.Reply)
	if n == 0 || n > MaxReplyChars {
		return nil, fmt.Errorf("%w: reply length %d", ErrInvalidOutput, n)
	}
	if !SuggestIsValid(out.Suggest) {
		return nil, fmt.Errorf("%w: suggest", ErrInvalidOutput)
	}
	byID := make(map[uuid.UUID]Fragment, len(retrieved))
	for _, f := range retrieved {
		byID[f.ArticleID] = f
	}
	seen := make(map[uuid.UUID]bool, len(out.Sources))
	var kept []Fragment
	for _, s := range out.Sources {
		id, err := uuid.Parse(s)
		if err != nil {
			continue
		}
		f, ok := byID[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		kept = append(kept, f)
		if len(kept) == MaxSources {
			break
		}
	}
	return kept, nil
}

// Turn is a stored dialogue turn. SealedText is nil for crisis and degraded
// assistant turns.
type Turn struct {
	ID             uuid.UUID
	MemberID       uuid.UUID
	Role           string
	ReplyTo        *uuid.UUID
	SealedText     []byte
	Crisis         bool
	Degraded       bool
	Sources        []SourceRef
	Suggest        string
	EscalationID   *uuid.UUID
	IdempotencyKey *uuid.UUID
	Helped         *bool
	CreatedAt      time.Time
}

// Reply is the result of one assistant message (contract AssistantReply).
type Reply struct {
	MessageID      uuid.UUID
	Text           *string
	Crisis         bool
	CrisisPhones   []CrisisPhone
	EscalationID   *uuid.UUID
	Degraded       bool
	Sources        []SourceRef
	Suggest        string
	DisclaimerKey  string
	RemainingToday int // -1 when unknown (crisis without consent)
}

// ReplyRatedData is the payload of assistant.ReplyRated v1: IDs and the
// rating only, never text.
type ReplyRatedData struct {
	MessageID uuid.UUID `json:"message_id"`
	Helped    bool      `json:"helped"`
}
