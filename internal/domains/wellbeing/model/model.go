// Package model holds wellbeing domain types and invariants. No I/O.
package model

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Instruments in the weekly questionnaire.
const (
	InstrumentPHQ4 = "phq4"
	InstrumentPSS4 = "pss4"
)

// Check-in flags. Never a diagnosis: the client shows locale feedback.
const (
	FlagNone     = "none"
	FlagElevated = "elevated"
	FlagCrisis   = "crisis"
)

// Suggestions offered after a check-in (locale keys / navigation targets).
const (
	SuggestionPractice     = "practice"
	SuggestionTea          = "tea"
	SuggestionTalk         = "talk"
	SuggestionPsychologist = "psychologist"
	SuggestionNone         = "none"
)

// Languages for practices.
const (
	LangKK = "kk"
	LangRU = "ru"
	LangEN = "en"
)

// AnswersField is the associated-data field name for sealed check-in answers (ADR-0009).
const AnswersField = "wellbeing.checkins.answers"

// TimerReminder is the platform timer kind for weekly check-in reminders (ADR-0020).
const TimerReminder = "wellbeing.Reminder"

// Domain event types (contracts/events).
const (
	EventCheckinCompleted         = "wellbeing.CheckinCompleted"
	EventCheckinCompletedVersion  = 1
	EventDeliveryRequested        = "notify.DeliveryRequested"
	EventDeliveryRequestedVersion = 1
)

// ReminderTemplate is the notify template key for the weekly reminder.
const ReminderTemplate = "wellbeing.checkin_reminder"

// ReminderClass is the notify delivery class for check-in reminders.
const ReminderClass = "reminder"

// EscalationSourceCheckin is passed to CrisisOpener when flag=crisis.
const EscalationSourceCheckin = "checkin"

const (
	defaultTimeZone     = "Asia/Almaty"
	defaultHistoryWeeks = 8
	maxHistoryWeeks     = 52
	phq4MaxScore        = 12
	pss4MaxScore        = 16
)

var (
	// ErrUnauthorized means the caller has no member session.
	ErrUnauthorized = errors.New("wellbeing: unauthorized")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("wellbeing: invalid input")
	// ErrNotFound means the schedule or check-in was not found.
	ErrNotFound = errors.New("wellbeing: not found")
)

// CrisisPhone is a configured crisis line shown when flag=crisis.
type CrisisPhone struct {
	Label  string
	Number string
}

// QuestionOption is one answer button for a questionnaire item.
type QuestionOption struct {
	Value   int
	TextKey string
}

// Question is one questionnaire item (locale keys only; texts live in locale files).
type Question struct {
	ID         string
	Instrument string
	TextKey    string
	Options    []QuestionOption
}

// InstrumentThresholds are score cut-offs for one instrument (never diagnoses).
type InstrumentThresholds struct {
	ElevatedAt int
	CrisisAt   int
}

// Feedback is the non-diagnostic result copy and suggestion for a flag.
type Feedback struct {
	FeedbackKey string
	Suggestion  string
}

// Questionnaire is the configured question set for one version.
type Questionnaire struct {
	Version   string
	Questions []Question
}

// Answer is one submitted answer.
type Answer struct {
	QuestionID string
	Value      int
}

// Schedule is the member's weekly reminder preference.
type Schedule struct {
	MemberID  uuid.UUID
	Weekday   int // ISO: 1 Monday … 7 Sunday
	Hour      int // local hour in the deployment time zone
	Enabled   bool
	UpdatedAt time.Time
}

// Checkin is a stored completed check-in (answers remain sealed).
type Checkin struct {
	ID                   uuid.UUID
	MemberID             uuid.UUID
	QuestionnaireVersion string
	SealedAnswers        []byte
	PHQ4                 int
	PSS4                 int
	Flag                 string
	IdempotencyKey       uuid.UUID
	CompletedAt          time.Time
}

// CheckinResult is the API-facing scored result (never a diagnosis).
type CheckinResult struct {
	ID           uuid.UUID
	Flag         string
	FeedbackKey  string
	Suggestion   string
	EscalationID *uuid.UUID
	CrisisPhones []CrisisPhone
	PHQ4         int
	PSS4         int
	CompletedAt  time.Time
}

// HistoryPoint is one chart point (scores only; no answers).
type HistoryPoint struct {
	CompletedAt time.Time
	PHQ4        int
	PSS4        int
}

// Practice is an audio practice with a URL built from the configured base + object key.
type Practice struct {
	ID              uuid.UUID
	Lang            string
	TitleKey        string
	DurationSeconds int
	ObjectKey       string
	URL             string
}

// CheckinCompletedData is the payload of wellbeing.CheckinCompleted.v1 (flag only; no scores).
type CheckinCompletedData struct {
	MemberID  uuid.UUID `json:"member_id"`
	CheckinID uuid.UUID `json:"checkin_id"`
	Flag      string    `json:"flag"`
}

// DeliveryRequestedData is the payload of notify.DeliveryRequested.v1 published by reminders.
type DeliveryRequestedData struct {
	RequestID uuid.UUID         `json:"request_id"`
	Recipient DeliveryRecipient `json:"recipient"`
	Class     string            `json:"class"`
	Template  string            `json:"template"`
	Params    map[string]string `json:"params,omitempty"`
}

// DeliveryRecipient is exactly one recipient form (member for reminders).
type DeliveryRecipient struct {
	MemberID *uuid.UUID `json:"member_id,omitempty"`
}

// ScoreInput is the validated answer set ready for scoring.
type ScoreInput struct {
	Answers []Answer
}

// ScoreOutput is the scored result before persistence.
type ScoreOutput struct {
	PHQ4 int
	PSS4 int
	Flag string
}

// FlagIsValid reports whether f is a known check-in flag.
func FlagIsValid(f string) bool {
	switch f {
	case FlagNone, FlagElevated, FlagCrisis:
		return true
	default:
		return false
	}
}

// SuggestionIsValid reports whether s is a known suggestion.
func SuggestionIsValid(s string) bool {
	switch s {
	case SuggestionPractice, SuggestionTea, SuggestionTalk, SuggestionPsychologist, SuggestionNone:
		return true
	default:
		return false
	}
}

// LangIsValid reports whether lang is kk or ru.
func LangIsValid(lang string) bool {
	return lang == LangKK || lang == LangRU || lang == LangEN
}

// InstrumentIsValid reports whether instrument is phq4 or pss4.
func InstrumentIsValid(instrument string) bool {
	return instrument == InstrumentPHQ4 || instrument == InstrumentPSS4
}

// DefaultTimeZone is Asia/Almaty (UTC+5 since March 2024). Deployments must ship current tzdata.
func DefaultTimeZone() string { return defaultTimeZone }

// DefaultHistoryWeeks is the default chart window.
func DefaultHistoryWeeks() int { return defaultHistoryWeeks }

// ClampHistoryWeeks bounds the requested weeks to [1, 52], defaulting empty to 8.
func ClampHistoryWeeks(weeks int) int {
	if weeks <= 0 {
		return defaultHistoryWeeks
	}
	if weeks > maxHistoryWeeks {
		return maxHistoryWeeks
	}
	return weeks
}

// ValidateSchedule checks weekday (ISO 1–7) and hour (0–23).
func ValidateSchedule(weekday, hour int) error {
	if weekday < 1 || weekday > 7 {
		return fmt.Errorf("%w: weekday", ErrInvalidInput)
	}
	if hour < 0 || hour > 23 {
		return fmt.Errorf("%w: hour", ErrInvalidInput)
	}
	return nil
}

// NextOccurrence returns the next local weekday/hour strictly after now in loc.
// Weekday is ISO (1=Monday … 7=Sunday). Asia/Almaty has no DST; keep tzdata current.
func NextOccurrence(now time.Time, loc *time.Location, weekday, hour int) time.Time {
	local := now.In(loc)
	target := isoToGoWeekday(weekday)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), hour, 0, 0, 0, loc)
	for candidate.Weekday() != target || !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
		candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day(), hour, 0, 0, 0, loc)
	}
	return candidate
}

// NextWeeklyOccurrence advances from a due instant by one week at the same local wall time.
func NextWeeklyOccurrence(due time.Time, loc *time.Location, weekday, hour int) time.Time {
	local := due.In(loc)
	next := local.AddDate(0, 0, 7)
	next = time.Date(next.Year(), next.Month(), next.Day(), hour, 0, 0, 0, loc)
	// Guard against rare calendar skew: ensure weekday still matches.
	if next.Weekday() != isoToGoWeekday(weekday) {
		return NextOccurrence(due, loc, weekday, hour)
	}
	if !next.After(due) {
		return NextOccurrence(due, loc, weekday, hour)
	}
	return next
}

func isoToGoWeekday(iso int) time.Weekday {
	if iso == 7 {
		return time.Sunday
	}
	return time.Weekday(iso)
}

// ValidateAnswers ensures every question is answered exactly once with a legal option value
// and that questionnaireVersion matches the configured current version.
func ValidateAnswers(q Questionnaire, version string, answers []Answer) error {
	if version == "" || version != q.Version {
		return fmt.Errorf("%w: questionnaire version", ErrInvalidInput)
	}
	if len(answers) != len(q.Questions) {
		return fmt.Errorf("%w: answer count", ErrInvalidInput)
	}
	byID := make(map[string]Question, len(q.Questions))
	for _, qu := range q.Questions {
		byID[qu.ID] = qu
	}
	seen := make(map[string]struct{}, len(answers))
	for _, a := range answers {
		if _, dup := seen[a.QuestionID]; dup {
			return fmt.Errorf("%w: duplicate answer", ErrInvalidInput)
		}
		seen[a.QuestionID] = struct{}{}
		qu, ok := byID[a.QuestionID]
		if !ok {
			return fmt.Errorf("%w: unknown question", ErrInvalidInput)
		}
		if !optionAllowed(qu, a.Value) {
			return fmt.Errorf("%w: option value", ErrInvalidInput)
		}
	}
	if len(seen) != len(q.Questions) {
		return fmt.Errorf("%w: missing answer", ErrInvalidInput)
	}
	return nil
}

func optionAllowed(q Question, value int) bool {
	for _, o := range q.Options {
		if o.Value == value {
			return true
		}
	}
	return false
}

func maxOptionValue(q Question) int {
	max := 0
	for i, o := range q.Options {
		if i == 0 || o.Value > max {
			max = o.Value
		}
	}
	return max
}

// Score computes PHQ-4 (0–12) and PSS-4 (0–16) with optional reverse items and flag thresholds.
// Crisis also when crisisItemID is answered at its maximum option. Never produces a diagnosis.
func Score(
	q Questionnaire,
	answers []Answer,
	reverseIDs []string,
	crisisItemID string,
	thresholds map[string]InstrumentThresholds,
) (ScoreOutput, error) {
	byID := make(map[string]Question, len(q.Questions))
	for _, qu := range q.Questions {
		byID[qu.ID] = qu
	}
	answerByID := make(map[string]int, len(answers))
	for _, a := range answers {
		answerByID[a.QuestionID] = a.Value
	}

	var phq, pss int
	for _, qu := range q.Questions {
		v, ok := answerByID[qu.ID]
		if !ok {
			return ScoreOutput{}, fmt.Errorf("%w: missing answer", ErrInvalidInput)
		}
		contrib := v
		if slices.Contains(reverseIDs, qu.ID) {
			contrib = maxOptionValue(qu) - v
		}
		switch qu.Instrument {
		case InstrumentPHQ4:
			phq += contrib
		case InstrumentPSS4:
			pss += contrib
		default:
			return ScoreOutput{}, fmt.Errorf("%w: instrument", ErrInvalidInput)
		}
	}
	if phq < 0 || phq > phq4MaxScore {
		return ScoreOutput{}, fmt.Errorf("%w: phq4 score", ErrInvalidInput)
	}
	if pss < 0 || pss > pss4MaxScore {
		return ScoreOutput{}, fmt.Errorf("%w: pss4 score", ErrInvalidInput)
	}

	flag := FlagNone
	if crisisItemID != "" {
		if qu, ok := byID[crisisItemID]; ok {
			if v, ok := answerByID[crisisItemID]; ok && v == maxOptionValue(qu) {
				flag = FlagCrisis
			}
		}
	}
	flag = raiseFlag(flag, InstrumentPHQ4, phq, thresholds)
	flag = raiseFlag(flag, InstrumentPSS4, pss, thresholds)

	return ScoreOutput{PHQ4: phq, PSS4: pss, Flag: flag}, nil
}

func raiseFlag(current, instrument string, score int, thresholds map[string]InstrumentThresholds) string {
	th, ok := thresholds[instrument]
	if !ok {
		return current
	}
	severity := current
	if th.CrisisAt > 0 && score >= th.CrisisAt {
		severity = FlagCrisis
	} else if th.ElevatedAt > 0 && score >= th.ElevatedAt {
		if severity != FlagCrisis {
			severity = FlagElevated
		}
	}
	return maxFlag(current, severity)
}

func maxFlag(a, b string) string {
	rank := map[string]int{FlagNone: 0, FlagElevated: 1, FlagCrisis: 2}
	if rank[b] > rank[a] {
		return b
	}
	return a
}
