// Package model holds the analytics domain types: facts, the metrics shape,
// the period rules, the pseudonym function and small-cell suppression. It
// has no I/O.
//
// Analytics counts; it never knows who. A fact names people only through
// Pseudonym (HMAC-SHA256 with the analytics key), and every cell of a report
// built from fewer than k members is suppressed by Suppress, the one place
// that rule lives (ADR-0024).
package model

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
)

// Errors returned by the service; the HTTP layer maps them to problems.
var (
	ErrUnauthorized = errors.New("analytics: unauthorized")
	ErrForbidden    = errors.New("analytics: forbidden")
	ErrInvalidInput = errors.New("analytics: invalid input")
)

// Consumed event types (ADR-0024 addendum) and account deletion.
const (
	EventMemberActiveDay         = "membership.MemberActiveDay"
	EventTeaRequested            = "circle.TeaRequested"
	EventMatchAccepted           = "circle.MatchAccepted"
	EventAcquaintanceEstablished = "circle.AcquaintanceEstablished"
	EventSosRaised               = "emergency.SosRaised"
	EventSosResponseAccepted     = "emergency.SosResponseAccepted"
	EventSosClosed               = "emergency.SosClosed"
	EventEscalationOpened        = "safety.EscalationOpened"
	EventEscalationTaken         = "safety.EscalationTaken"
	EventQuestionAnswered        = "assistant.QuestionAnswered"
	EventReplyRated              = "assistant.ReplyRated"
	EventDataDeletionRequested   = "privacy.DataDeletionRequested"
)

// ConsumedEvents are the events turned into facts, in contract order.
var ConsumedEvents = []string{
	EventMemberActiveDay,
	EventTeaRequested, EventMatchAccepted, EventAcquaintanceEstablished,
	EventSosRaised, EventSosResponseAccepted, EventSosClosed,
	EventEscalationOpened, EventEscalationTaken,
	EventQuestionAnswered, EventReplyRated,
}

// TimerRetention is the daily retention sweep (ADR-0020); its subject is RetentionSubject.
const TimerRetention = "analytics.Retention"

// RetentionSubject is the fixed subject ID of the retention timer.
var RetentionSubject = uuid.MustParse("019a3000-0000-7000-8000-0000000a4a1c")

// Retention and report rules (ADR-0024).
const (
	// FactRetentionMonths is how long facts are kept before the weekly roll-up.
	FactRetentionMonths = 24
	// MaxPeriodMonths is the longest report period.
	MaxPeriodMonths = 12
	// DefaultK is the small-cell threshold when configuration sets none.
	DefaultK = 5
	// EscalationTakenWithin is the target of requirements section 9.
	EscalationTakenWithin = 15 * time.Minute
	// DefaultTimeZone buckets weeks and months when Settings has none.
	DefaultTimeZone = "Asia/Almaty"
)

// Codes stored with facts: short enum values only.
const (
	CodeHelped    = "helped"
	CodeNotHelped = "not_helped"
)

// Fact is one stored fact. Actor and Subject are 32-byte pseudonyms or nil.
type Fact struct {
	ID         uuid.UUID
	Type       string
	OccurredAt time.Time
	Code       string
	Actor      []byte
	Subject    []byte
}

// Pseudonym returns HMAC-SHA256(key, id) over the canonical UUID text. It is
// the actor of a member and, with SubjectPseudonym, the only form in which
// analytics stores an ID. Without the key it cannot be reversed or linked.
func Pseudonym(key []byte, id uuid.UUID) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(id.String()))
	return m.Sum(nil)
}

// SubjectPseudonym is the pseudonym of an object (SOS, escalation, match,
// request, message). The prefix keeps it apart from member pseudonyms.
func SubjectPseudonym(key []byte, id uuid.UUID) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("subject|" + id.String()))
	return m.Sum(nil)
}

// LockOrder sorts facts by the pseudonym the store locks when it inserts
// each (the actor, else the subject; see InsertFact in the adapter). The
// facts of one event are inserted in this order, so two transactions that
// lock the same two members (a pair event delivered twice with the members
// swapped, or two events of one pair) take the locks in the same order and
// cannot deadlock.
func LockOrder(facts []Fact) {
	key := func(f Fact) []byte {
		if f.Actor != nil {
			return f.Actor
		}
		return f.Subject
	}
	slices.SortStableFunc(facts, func(a, b Fact) int { return bytes.Compare(key(a), key(b)) })
}

// RetentionCutoff is the first instant whose facts are kept at now: the
// Monday 00:00 in loc that starts the week of now minus FactRetentionMonths.
// Cutting at a week start rolls every week up whole and only once.
func RetentionCutoff(now time.Time, loc *time.Location) time.Time {
	t := now.In(loc).AddDate(0, -FactRetentionMonths, 0)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

// NextDailyRun returns the first daily run at the time of day `at` (UTC)
// strictly after now.
func NextDailyRun(now time.Time, at time.Duration) time.Time {
	now = now.UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	next := day.Add(at)
	if !next.After(now) {
		next = day.AddDate(0, 0, 1).Add(at)
	}
	return next
}
