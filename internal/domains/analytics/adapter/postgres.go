// Package adapter implements the analytics ports on PostgreSQL (schema
// analytics, sqlc in ../store) with the platform hooks it needs
// (processed_events, audit_log, timers), the JetStream consumer of the
// metric events and the retention timer handler.
package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/analytics/model"
	"birge/internal/domains/analytics/port"
	analyticsstore "birge/internal/domains/analytics/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store and port.MemberEraser on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction.
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction (the timer worker's).
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{q: analyticsstore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	q        *analyticsstore.Queries
	platform *platformstore.Queries
}

// MarkProcessed implements port.Tx.
func (t *postgresTx) MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	n, err := t.platform.MarkEventProcessed(ctx, platformstore.MarkEventProcessedParams{
		Consumer: consumer, EventID: eventID,
	})
	if err != nil {
		return false, fmt.Errorf("mark processed: %w", err)
	}
	return n == 1, nil
}

// Advisory lock classes of LockPseudonym (analytics_facts.sql): actors and
// subjects never share a key.
const (
	lockClassActor   = "analytics.actor"
	lockClassSubject = "analytics.subject"
)

// InsertFact implements port.Tx. It first takes the transaction lock of the
// fact's actor (or, without one, of its subject), the lock EraseActor takes
// before it deletes, so the erased check and the erasure never interleave.
// A transaction storing several facts must insert them in model.LockOrder,
// so two of them never wait on each other's locks.
func (t *postgresTx) InsertFact(ctx context.Context, f model.Fact) error {
	class, key := lockClassActor, f.Actor
	if key == nil {
		class, key = lockClassSubject, f.Subject
	}
	if key != nil {
		if err := t.q.LockPseudonym(ctx, analyticsstore.LockPseudonymParams{Class: class, Pseudonym: key}); err != nil {
			return fmt.Errorf("lock pseudonym: %w", err)
		}
	}
	var code *string
	if f.Code != "" {
		code = &f.Code
	}
	if err := t.q.InsertFact(ctx, analyticsstore.InsertFactParams{
		ID: f.ID, EventType: f.Type, OccurredAt: f.OccurredAt, Code: code, Actor: f.Actor, Subject: f.Subject,
	}); err != nil {
		return fmt.Errorf("insert fact: %w", err)
	}
	return nil
}

// CountFacts implements port.Tx.
func (t *postgresTx) CountFacts(ctx context.Context, from, to time.Time, types []string) (map[string]port.FactCounts, error) {
	rows, err := t.q.CountFacts(ctx, analyticsstore.CountFactsParams{FromAt: from, ToAt: to, EventTypes: types})
	if err != nil {
		return nil, fmt.Errorf("count facts: %w", err)
	}
	out := make(map[string]port.FactCounts, len(rows))
	for _, r := range rows {
		out[r.EventType] = port.FactCounts{Facts: int(r.Facts), Subjects: int(r.Subjects), Members: int(r.Members)}
	}
	return out, nil
}

// ActiveMembers implements port.Tx.
func (t *postgresTx) ActiveMembers(ctx context.Context, unit, tz string, from, to time.Time) (map[time.Time]int, error) {
	out := map[time.Time]int{}
	add := func(bucket time.Time, valid bool, members int32) {
		if valid {
			out[time.Date(bucket.Year(), bucket.Month(), bucket.Day(), 0, 0, 0, 0, time.UTC)] = int(members)
		}
	}
	switch unit {
	case "week":
		rows, err := t.q.ActiveMembersByWeek(ctx, analyticsstore.ActiveMembersByWeekParams{Tz: tz, FromAt: from, ToAt: to})
		if err != nil {
			return nil, fmt.Errorf("active members by week: %w", err)
		}
		for _, r := range rows {
			add(r.Bucket.Time, r.Bucket.Valid, r.Members)
		}
	case "month":
		rows, err := t.q.ActiveMembersByMonth(ctx, analyticsstore.ActiveMembersByMonthParams{Tz: tz, FromAt: from, ToAt: to})
		if err != nil {
			return nil, fmt.Errorf("active members by month: %w", err)
		}
		for _, r := range rows {
			add(r.Bucket.Time, r.Bucket.Valid, r.Members)
		}
	default:
		return nil, fmt.Errorf("active members: unknown unit %q", unit)
	}
	return out, nil
}

// Retention implements port.Tx.
func (t *postgresTx) Retention(ctx context.Context, from, to, now time.Time) (port.RetentionCounts, error) {
	r, err := t.q.Retention(ctx, analyticsstore.RetentionParams{NowAt: now, FromAt: from, ToAt: to})
	if err != nil {
		return port.RetentionCounts{}, fmt.Errorf("retention: %w", err)
	}
	return port.RetentionCounts{
		Eligible4w: int(r.Eligible4w), Retained4w: int(r.Retained4w),
		Eligible8w: int(r.Eligible8w), Retained8w: int(r.Retained8w),
	}, nil
}

// SosOutcomes implements port.Tx.
func (t *postgresTx) SosOutcomes(ctx context.Context, from, to time.Time) (port.SosCounts, error) {
	r, err := t.q.SosOutcomes(ctx, analyticsstore.SosOutcomesParams{FromAt: from, ToAt: to})
	if err != nil {
		return port.SosCounts{}, fmt.Errorf("sos outcomes: %w", err)
	}
	return port.SosCounts{
		Raised: int(r.Raised), RaisedMembers: int(r.RaisedMembers), Closed: int(r.Closed),
		Responded: int(r.Responded), RespondedMembers: int(r.RespondedMembers),
		MedianMinutesToFirstResponse: r.MedianMinutes,
	}, nil
}

// EscalationOutcomes implements port.Tx.
func (t *postgresTx) EscalationOutcomes(ctx context.Context, from, to time.Time, within time.Duration) (port.EscalationCounts, error) {
	r, err := t.q.EscalationOutcomes(ctx, analyticsstore.EscalationOutcomesParams{
		WithinSeconds: within.Seconds(), FromAt: from, ToAt: to,
	})
	if err != nil {
		return port.EscalationCounts{}, fmt.Errorf("escalation outcomes: %w", err)
	}
	return port.EscalationCounts{Opened: int(r.Opened), Members: int(r.Members), TakenWithin: int(r.TakenWithin)}, nil
}

// RightsOutcomes implements port.Tx.
func (t *postgresTx) RightsOutcomes(ctx context.Context, from, to time.Time) (port.RightsCounts, error) {
	r, err := t.q.RightsOutcomes(ctx, analyticsstore.RightsOutcomesParams{FromAt: from, ToAt: to})
	if err != nil {
		return port.RightsCounts{}, fmt.Errorf("rights outcomes: %w", err)
	}
	return port.RightsCounts{
		AnsweredFromKnowledge: int(r.AnsweredFromKnowledge), KnowledgeMembers: int(r.KnowledgeMembers),
		Rated: int(r.Rated), Helped: int(r.Helped), RatedMembers: int(r.RatedMembers),
	}, nil
}

// RollUpAndDelete implements port.Tx. The roll-up runs before the delete in
// the same transaction, so no fact is deleted without being counted.
func (t *postgresTx) RollUpAndDelete(ctx context.Context, tz string, before, erasedBefore time.Time) (int64, error) {
	if _, err := t.q.RollUpWeeks(ctx, analyticsstore.RollUpWeeksParams{Tz: tz, Before: before}); err != nil {
		return 0, fmt.Errorf("roll up weeks: %w", err)
	}
	n, err := t.q.DeleteFactsBefore(ctx, before)
	if err != nil {
		return 0, fmt.Errorf("delete old facts: %w", err)
	}
	if _, err := t.q.DeleteErasedBefore(ctx, erasedBefore); err != nil {
		return 0, fmt.Errorf("delete old erased pseudonyms: %w", err)
	}
	if _, err := t.q.DeleteErasedSubjectsBefore(ctx, erasedBefore); err != nil {
		return 0, fmt.Errorf("delete old erased subjects: %w", err)
	}
	return n, nil
}

// ScheduleRetention implements port.Tx (dedup key: one run per day).
func (t *postgresTx) ScheduleRetention(ctx context.Context, at time.Time) error {
	_, err := timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerRetention, SubjectID: model.RetentionSubject,
		DedupKey: "retention|" + at.UTC().Format(time.DateOnly), DueAt: at, Payload: map[string]string{},
	})
	return err
}

// Audit implements port.Tx.
func (t *postgresTx) Audit(ctx context.Context, e port.AuditEntry) error {
	return audit.Record(ctx, t.platform, audit.Entry{
		ActorID: e.ActorID, ActorRole: e.ActorRole, Action: e.Action, ObjectType: e.ObjectType,
	})
}

// EraseActor implements port.MemberEraser. Order matters:
//  1. lock her actor, then the subjects of her SOS, escalations and answers
//     (sorted), the locks InsertFact takes; each later statement takes a new
//     snapshot under READ COMMITTED, so it sees every fact committed by a
//     transaction that held one of the locks first;
//  2. record those subjects as erased, so a late closing, taking or rating
//     (no actor) is dropped;
//  3. delete the actor-less facts about those subjects, then her facts;
//  4. record her pseudonym as erased and write the audit entry.
func (s *PostgresStore) EraseActor(ctx context.Context, memberID uuid.UUID, actor []byte, now time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q, platform := analyticsstore.New(tx), platformstore.New(tx)
		if err := q.LockPseudonym(ctx, analyticsstore.LockPseudonymParams{Class: lockClassActor, Pseudonym: actor}); err != nil {
			return fmt.Errorf("analytics: lock actor: %w", err)
		}
		if err := q.LockErasedSubjects(ctx, analyticsstore.LockErasedSubjectsParams{Class: lockClassSubject, Actor: actor}); err != nil {
			return fmt.Errorf("analytics: lock subjects: %w", err)
		}
		if err := q.MarkSubjectsErased(ctx, analyticsstore.MarkSubjectsErasedParams{Actor: actor, ErasedAt: now}); err != nil {
			return fmt.Errorf("analytics: mark subjects erased: %w", err)
		}
		if _, err := q.DeleteDependentFacts(ctx, actor); err != nil {
			return fmt.Errorf("analytics: delete dependent facts: %w", err)
		}
		if _, err := q.DeleteActorFacts(ctx, actor); err != nil {
			return fmt.Errorf("analytics: delete facts: %w", err)
		}
		if err := q.MarkActorErased(ctx, analyticsstore.MarkActorErasedParams{Actor: actor, ErasedAt: now}); err != nil {
			return fmt.Errorf("analytics: mark erased: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platform, "analytics", memberID)
	})
}

var (
	_ port.Store        = (*PostgresStore)(nil)
	_ port.MemberEraser = (*PostgresStore)(nil)
	_ port.Tx           = (*postgresTx)(nil)
)
