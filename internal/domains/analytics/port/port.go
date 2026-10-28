// Package port defines the interfaces the analytics service needs: its store
// (facts, roll-ups, erased pseudonyms, processed_events, audit, the retention
// timer) and the wellbeing cohort aggregates, implemented in internal/wiring.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/analytics/model"
)

// Cohorts returns the wellbeing stress and isolation aggregates for the
// members whose first check-in falls in [from, to) (wellbeing
// service.CohortAggregates, ADR-0024). Implementations return counts, means
// and medians only, never per-member scores.
type Cohorts interface {
	CohortAggregates(ctx context.Context, from, to time.Time) (model.CohortAggregates, error)
}

// Store is the persistence port of the analytics schema and platform hooks.
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	// MarkProcessed records eventID for consumer; newly is false for a duplicate.
	MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (newly bool, err error)
	// InsertFact stores f unless its actor was erased (then it is dropped silently).
	InsertFact(ctx context.Context, f model.Fact) error

	// CountFacts returns, per event type, facts, distinct subjects and
	// distinct actors in [from, to).
	CountFacts(ctx context.Context, from, to time.Time, types []string) (map[string]FactCounts, error)
	// ActiveMembers returns distinct active members per bucket start (a
	// date in tz); unit is "week" or "month".
	ActiveMembers(ctx context.Context, unit, tz string, from, to time.Time) (map[time.Time]int, error)
	// Retention returns the 4- and 8-week retention of the members whose
	// first active day is in [from, to), counting only windows ended by now.
	Retention(ctx context.Context, from, to, now time.Time) (RetentionCounts, error)
	// SosOutcomes returns the outcomes of the SOS raised in [from, to).
	SosOutcomes(ctx context.Context, from, to time.Time) (SosCounts, error)
	// EscalationOutcomes returns the outcomes of the escalations opened in [from, to).
	EscalationOutcomes(ctx context.Context, from, to time.Time, within time.Duration) (EscalationCounts, error)

	// RollUpAndDelete rolls the facts before `before` into weekly counts
	// (weeks in tz), deletes those facts and the erased pseudonyms older than
	// erasedBefore. It returns how many facts were deleted.
	RollUpAndDelete(ctx context.Context, tz string, before, erasedBefore time.Time) (int64, error)
	// ScheduleRetention schedules the next analytics.Retention run (deduplicated per day).
	ScheduleRetention(ctx context.Context, at time.Time) error

	// Audit writes one platform.audit_log entry in this transaction.
	Audit(ctx context.Context, e AuditEntry) error
}

// FactCounts are the counts of one event type in a period.
type FactCounts struct {
	Facts, Subjects, Members int
}

// RetentionCounts are the eligible and retained members for weeks 4 and 8.
type RetentionCounts struct {
	Eligible4w, Retained4w, Eligible8w, Retained8w int
}

// SosCounts are the outcomes of the SOS raised in a period.
type SosCounts struct {
	Raised, RaisedMembers, Closed int
	Responded, RespondedMembers   int
	MedianMinutesToFirstResponse  float64
}

// EscalationCounts are the outcomes of the escalations opened in a period.
type EscalationCounts struct {
	Opened, Members, TakenWithin int
}

// AuditEntry is one platform.audit_log row written with a read or export.
type AuditEntry struct {
	ActorID    uuid.UUID
	ActorRole  string
	Action     string
	ObjectType string
}

// MemberEraser erases a member's analytics facts on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6).
type MemberEraser interface {
	// EraseActor, in one transaction: deletes the facts of actor (and the
	// actor-less closed/taken facts of her SOS and escalations), records the
	// pseudonym as erased so late events are not counted, and writes the
	// audit entry once (memberID is its object). Repeating it changes nothing.
	EraseActor(ctx context.Context, memberID uuid.UUID, actor []byte, now time.Time) error
}
