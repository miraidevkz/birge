// Package service implements wellbeing use cases: schedules, check-ins, history and practices.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/wellbeing/model"
	"birge/internal/domains/wellbeing/port"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
)

// Settings holds wellbeing runtime configuration (questionnaire, thresholds, phones, URLs).
type Settings struct {
	// Questionnaire is the current question set (version + items with locale keys).
	Questionnaire model.Questionnaire
	// ReverseScoreQuestionIDs are items whose contribution is maxOption−value (typically PSS).
	ReverseScoreQuestionIDs []string
	// CrisisItemQuestionID, when answered at its maximum option, forces flag=crisis.
	CrisisItemQuestionID string
	// Thresholds maps instrument → ElevatedAt / CrisisAt score cut-offs.
	Thresholds map[string]model.InstrumentThresholds
	// FeedbackByFlag maps flag → non-diagnostic feedback key and suggestion.
	FeedbackByFlag map[string]model.Feedback
	// TimeZone is the deployment IANA zone (default Asia/Almaty). Hosts need current tzdata.
	TimeZone string
	// PracticesBaseURL is the in-jurisdiction object-storage base for practice audio URLs.
	PracticesBaseURL string
	// CrisisPhones are returned whenever flag=crisis (even if CrisisOpener fails).
	CrisisPhones []model.CrisisPhone
}

// Service is the wellbeing application service.
type Service struct {
	Store    port.Store
	Sealer   port.Sealer
	Crisis   port.CrisisOpener
	Clock    clock.Clock
	Logger   *slog.Logger
	Settings Settings
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) timeZone() (*time.Location, error) {
	name := s.Settings.TimeZone
	if name == "" {
		name = model.DefaultTimeZone()
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("wellbeing: load time zone %q: %w", name, err)
	}
	return loc, nil
}

func (s *Service) feedbackFor(flag string) (model.Feedback, error) {
	if s.Settings.FeedbackByFlag == nil {
		return model.Feedback{}, fmt.Errorf("%w: feedback config", model.ErrInvalidInput)
	}
	fb, ok := s.Settings.FeedbackByFlag[flag]
	if !ok || fb.FeedbackKey == "" || !model.SuggestionIsValid(fb.Suggestion) {
		return model.Feedback{}, fmt.Errorf("%w: feedback for flag", model.ErrInvalidInput)
	}
	return fb, nil
}

func (s *Service) requireMember(memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return model.ErrUnauthorized
	}
	return nil
}

// GetQuestionnaire returns the configured questionnaire (locale keys only).
func (s *Service) GetQuestionnaire(ctx context.Context, memberID uuid.UUID) (model.Questionnaire, error) {
	_ = ctx
	if err := s.requireMember(memberID); err != nil {
		return model.Questionnaire{}, err
	}
	q := s.Settings.Questionnaire
	if q.Version == "" || len(q.Questions) == 0 {
		return model.Questionnaire{}, fmt.Errorf("%w: questionnaire not configured", model.ErrInvalidInput)
	}
	return q, nil
}

// GetSchedule returns the member's reminder schedule, or a zero schedule when unset.
func (s *Service) GetSchedule(ctx context.Context, memberID uuid.UUID) (model.Schedule, error) {
	if err := s.requireMember(memberID); err != nil {
		return model.Schedule{}, err
	}
	var out model.Schedule
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		sched, err := tx.GetSchedule(ctx, memberID)
		if errors.Is(err, model.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = sched
		return nil
	})
	return out, err
}

// SetSchedule stores the reminder preference and (re)schedules wellbeing.Reminder,
// or cancels the timer when disabled.
func (s *Service) SetSchedule(ctx context.Context, memberID uuid.UUID, weekday, hour int, enabled bool) (model.Schedule, error) {
	if err := s.requireMember(memberID); err != nil {
		return model.Schedule{}, err
	}
	if err := model.ValidateSchedule(weekday, hour); err != nil {
		return model.Schedule{}, err
	}
	loc, err := s.timeZone()
	if err != nil {
		return model.Schedule{}, err
	}

	now := s.Clock.Now().UTC()
	var out model.Schedule
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		sched, err := tx.UpsertSchedule(ctx, model.Schedule{
			MemberID:  memberID,
			Weekday:   weekday,
			Hour:      hour,
			Enabled:   enabled,
			UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if _, err := tx.CancelReminder(ctx, memberID, now); err != nil {
			return err
		}
		if enabled {
			due := model.NextOccurrence(now, loc, weekday, hour)
			if _, err := tx.ScheduleReminder(ctx, memberID, due); err != nil {
				return err
			}
		}
		out = sched
		return nil
	})
	if err != nil {
		return model.Schedule{}, err
	}
	s.log().Info("checkin schedule saved", "event", "ScheduleSaved", "enabled", enabled)
	return out, nil
}

// SubmitCheckin validates, scores, seals answers, stores the check-in and emits
// wellbeing.CheckinCompleted. On flag=crisis it opens an escalation; if that fails
// it still returns crisis phones and logs CrisisEscalationFailed (no answers in logs).
func (s *Service) SubmitCheckin(
	ctx context.Context,
	memberID uuid.UUID,
	idempotencyKey uuid.UUID,
	version string,
	answers []model.Answer,
) (model.CheckinResult, error) {
	if err := s.requireMember(memberID); err != nil {
		return model.CheckinResult{}, err
	}
	if idempotencyKey == uuid.Nil {
		return model.CheckinResult{}, fmt.Errorf("%w: idempotency key", model.ErrInvalidInput)
	}
	q := s.Settings.Questionnaire
	if err := model.ValidateAnswers(q, version, answers); err != nil {
		return model.CheckinResult{}, err
	}
	scored, err := model.Score(q, answers, s.Settings.ReverseScoreQuestionIDs, s.Settings.CrisisItemQuestionID, s.Settings.Thresholds)
	if err != nil {
		return model.CheckinResult{}, err
	}
	fb, err := s.feedbackFor(scored.Flag)
	if err != nil {
		return model.CheckinResult{}, err
	}
	if s.Sealer == nil {
		return model.CheckinResult{}, errors.New("wellbeing: sealer not configured")
	}

	checkinID, err := uuid.NewV7()
	if err != nil {
		return model.CheckinResult{}, fmt.Errorf("wellbeing: checkin id: %w", err)
	}
	plain, err := json.Marshal(answersPayload(answers))
	if err != nil {
		return model.CheckinResult{}, fmt.Errorf("marshal answers: %w", err)
	}
	ad := crypto.AssociatedData(model.AnswersField, checkinID)
	sealed, err := s.Sealer.Seal(ctx, memberID, plain, ad)
	if err != nil {
		return model.CheckinResult{}, fmt.Errorf("seal answers: %w", err)
	}

	now := s.Clock.Now().UTC()
	var (
		stored   model.Checkin
		inserted bool
	)
	err = s.Store.InTx(ctx, func(tx port.Tx) error {
		created, ok, err := tx.InsertCheckin(ctx, model.Checkin{
			ID:                   checkinID,
			MemberID:             memberID,
			QuestionnaireVersion: version,
			SealedAnswers:        sealed,
			PHQ4:                 scored.PHQ4,
			PSS4:                 scored.PSS4,
			Flag:                 scored.Flag,
			IdempotencyKey:       idempotencyKey,
			CompletedAt:          now,
		})
		if err != nil {
			return err
		}
		if !ok {
			existing, err := tx.GetCheckinByIdempotencyKey(ctx, memberID, idempotencyKey)
			if err != nil {
				return err
			}
			stored = existing
			inserted = false
			return nil
		}
		if err := tx.PublishCheckinCompleted(ctx, now, model.CheckinCompletedData{
			MemberID:  memberID,
			CheckinID: created.ID,
			Flag:      created.Flag,
		}); err != nil {
			return err
		}
		stored = created
		inserted = true
		return nil
	})
	if err != nil {
		return model.CheckinResult{}, err
	}

	resultFB, err := s.feedbackFor(stored.Flag)
	if err != nil {
		// Fall back to the feedback computed for this submission's flag when replaying
		// an older row against a config that still knows that flag.
		resultFB = fb
	}
	result := model.CheckinResult{
		ID:          stored.ID,
		Flag:        stored.Flag,
		FeedbackKey: resultFB.FeedbackKey,
		Suggestion:  resultFB.Suggestion,
		PHQ4:        stored.PHQ4,
		PSS4:        stored.PSS4,
		CompletedAt: stored.CompletedAt,
	}

	if stored.Flag == model.FlagCrisis {
		result.CrisisPhones = append([]model.CrisisPhone(nil), s.Settings.CrisisPhones...)
		if s.Crisis == nil {
			s.log().Error("crisis escalation failed", "event", "CrisisEscalationFailed", "checkin_id", stored.ID, "error", "crisis opener not configured")
		} else {
			escID, err := s.Crisis.OpenEscalation(ctx, memberID, model.EscalationSourceCheckin)
			if err != nil {
				s.log().Error("crisis escalation failed", "event", "CrisisEscalationFailed", "checkin_id", stored.ID, "error", err.Error())
			} else {
				result.EscalationID = &escID
			}
		}
	}

	if inserted {
		s.log().Info("checkin completed", "event", "CheckinCompleted", "checkin_id", stored.ID, "flag", stored.Flag)
	}
	return result, nil
}

type sealedAnswer struct {
	QuestionID string `json:"question_id"`
	Value      int    `json:"value"`
}

func answersPayload(answers []model.Answer) []sealedAnswer {
	out := make([]sealedAnswer, 0, len(answers))
	for _, a := range answers {
		out = append(out, sealedAnswer{QuestionID: a.QuestionID, Value: a.Value})
	}
	return out
}

// GetHistory returns chart points for the last weeks (oldest first).
func (s *Service) GetHistory(ctx context.Context, memberID uuid.UUID, weeks int) ([]model.HistoryPoint, error) {
	if err := s.requireMember(memberID); err != nil {
		return nil, err
	}
	weeks = model.ClampHistoryWeeks(weeks)
	now := s.Clock.Now().UTC()
	since := now.AddDate(0, 0, -7*weeks)

	var rows []model.Checkin
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		// weeks is clamped to <=52, so the limit fits int32.
		rows, err = tx.ListCheckinHistory(ctx, memberID, since, int32(weeks*2+8)) //nolint:gosec // G115: weeks <= 52
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.HistoryPoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.HistoryPoint{
			CompletedAt: r.CompletedAt,
			PHQ4:        r.PHQ4,
			PSS4:        r.PSS4,
		})
	}
	return out, nil
}

// CohortAggregates returns the stress and isolation aggregates for the cohort of
// members whose first check-in falls in [from, to): PSS-4 at that first check-in and
// at her check-in nearest to 8 weeks later (model.CohortFollowUp*). The follow-up may
// lie after to, so a cohort of a short period still gets its week-8 numbers once they
// exist. Only count, mean and median leave this method (ADR-0024); per-member scores
// stay inside wellbeing. There is no member check: this is not an HTTP operation and
// is reached only through the analytics port in internal/wiring, which enforces the
// staff role, MFA, audit and small-cell suppression.
func (s *Service) CohortAggregates(ctx context.Context, from, to time.Time) (model.CohortAggregates, error) {
	if from.IsZero() || to.IsZero() || !from.Before(to) {
		return model.CohortAggregates{}, fmt.Errorf("%w: cohort period", model.ErrInvalidInput)
	}
	var rows []model.CohortCheckin
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		rows, err = tx.ListCohortCheckins(ctx, from.UTC(), to.UTC(), to.UTC().Add(model.CohortFollowUpMax))
		return err
	})
	if err != nil {
		return model.CohortAggregates{}, err
	}
	return model.BuildCohortAggregates(rows), nil
}

// ListPractices returns audio practices for lang with URLs from PracticesBaseURL + object key.
func (s *Service) ListPractices(ctx context.Context, memberID uuid.UUID, lang string) ([]model.Practice, error) {
	if err := s.requireMember(memberID); err != nil {
		return nil, err
	}
	if !model.LangIsValid(lang) {
		return nil, fmt.Errorf("%w: lang", model.ErrInvalidInput)
	}
	base := strings.TrimRight(s.Settings.PracticesBaseURL, "/")
	if base == "" {
		return nil, fmt.Errorf("%w: practices base url", model.ErrInvalidInput)
	}

	var rows []model.Practice
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		rows, err = tx.ListPracticesByLang(ctx, lang)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Practice, 0, len(rows))
	for _, p := range rows {
		u, err := joinURL(base, p.ObjectKey)
		if err != nil {
			return nil, err
		}
		p.URL = u
		out = append(out, p)
	}
	return out, nil
}

func joinURL(base, objectKey string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("%w: practices base url", model.ErrInvalidInput)
	}
	u.Path = path.Join(u.Path, objectKey)
	return u.String(), nil
}

// HandleReminder fires wellbeing.Reminder in its own transaction (unit tests).
func (s *Service) HandleReminder(ctx context.Context, memberID uuid.UUID, dueAt time.Time) error {
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		return s.HandleReminderTx(ctx, tx, memberID, dueAt)
	})
}

// HandleReminderTx publishes notify.DeliveryRequested and schedules the next
// weekly occurrence on tx. The timer worker must pass its claimed transaction
// so CancelReminder can clear the active dedup row before ScheduleReminder.
func (s *Service) HandleReminderTx(ctx context.Context, tx port.Tx, memberID uuid.UUID, dueAt time.Time) error {
	if memberID == uuid.Nil {
		return fmt.Errorf("%w: member id", model.ErrInvalidInput)
	}
	loc, err := s.timeZone()
	if err != nil {
		return err
	}
	now := s.Clock.Now().UTC()

	sched, err := tx.GetSchedule(ctx, memberID)
	if errors.Is(err, model.ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sched.Enabled {
		return nil
	}

	requestID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("wellbeing: delivery request id: %w", err)
	}
	mid := memberID
	if err := tx.PublishDeliveryRequested(ctx, now, model.DeliveryRequestedData{
		RequestID: requestID,
		Recipient: model.DeliveryRecipient{MemberID: &mid},
		Class:     model.ReminderClass,
		Template:  model.ReminderTemplate,
	}); err != nil {
		return err
	}

	base := dueAt
	if base.IsZero() {
		base = now
	}
	next := model.NextWeeklyOccurrence(base, loc, sched.Weekday, sched.Hour)
	if _, err := tx.CancelReminder(ctx, memberID, now); err != nil {
		return err
	}
	if _, err := tx.ScheduleReminder(ctx, memberID, next); err != nil {
		return err
	}
	s.log().Info("checkin reminder sent", "event", "ReminderSent", "member_id", memberID)
	return nil
}
