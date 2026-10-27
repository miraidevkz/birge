// Package adapter implements the assistant ports on PostgreSQL (schema
// assistant) and platform hooks: the outbox for assistant.ReplyRated and the
// daily retention-purge timer. The model adapter lives in adapter/anthropic.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/port"
	assistantstore "birge/internal/domains/assistant/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction.
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(TxFrom(pgxTx))
	})
}

// TxFrom binds a port.Tx to an existing database transaction (timer worker).
func TxFrom(tx pgx.Tx) port.Tx {
	return &postgresTx{assistant: assistantstore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	assistant *assistantstore.Queries
	platform  *platformstore.Queries
}

// InsertExchange inserts the member turn and its answer; false when the
// member's idempotency key already exists (nothing is written then).
func (t *postgresTx) InsertExchange(ctx context.Context, member, assistant model.Turn) (bool, error) {
	_, err := t.assistant.InsertMemberTurn(ctx, assistantstore.InsertMemberTurnParams{
		ID:             member.ID,
		MemberID:       member.MemberID,
		TextSealed:     member.SealedText,
		CrisisFlag:     member.Crisis,
		IdempotencyKey: member.IdempotencyKey,
		CreatedAt:      member.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("insert member turn: %w", err)
	}
	sources, err := json.Marshal(assistant.Sources)
	if err != nil {
		return false, fmt.Errorf("marshal sources: %w", err)
	}
	if err := t.assistant.InsertAssistantTurn(ctx, assistantstore.InsertAssistantTurnParams{
		ID:           assistant.ID,
		MemberID:     assistant.MemberID,
		ReplyTo:      assistant.ReplyTo,
		TextSealed:   assistant.SealedText,
		CrisisFlag:   assistant.Crisis,
		Degraded:     assistant.Degraded,
		Sources:      sources,
		Suggest:      assistant.Suggest,
		EscalationID: assistant.EscalationID,
		CreatedAt:    assistant.CreatedAt,
	}); err != nil {
		return false, fmt.Errorf("insert assistant turn: %w", err)
	}
	return true, nil
}

// GetExchangeByIdempotencyKey returns the member turn for key and its answer.
func (t *postgresTx) GetExchangeByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Turn, model.Turn, error) {
	k := key
	m, err := t.assistant.GetMemberTurnByIdempotencyKey(ctx, assistantstore.GetMemberTurnByIdempotencyKeyParams{
		MemberID: memberID, IdempotencyKey: &k,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Turn{}, model.Turn{}, model.ErrNotFound
	}
	if err != nil {
		return model.Turn{}, model.Turn{}, err
	}
	id := m.ID
	a, err := t.assistant.GetReplyTo(ctx, &id)
	if errors.Is(err, pgx.ErrNoRows) {
		// Both turns are written in one transaction; a lone member turn means
		// the answer was purged by retention.
		return model.Turn{}, model.Turn{}, model.ErrNotFound
	}
	if err != nil {
		return model.Turn{}, model.Turn{}, err
	}
	mt, err := mapTurn(m)
	if err != nil {
		return model.Turn{}, model.Turn{}, err
	}
	at, err := mapTurn(a)
	if err != nil {
		return model.Turn{}, model.Turn{}, err
	}
	return mt, at, nil
}

// CountMemberTurnsSince counts the member's own turns since a time.
func (t *postgresTx) CountMemberTurnsSince(ctx context.Context, memberID uuid.UUID, since time.Time) (int, error) {
	n, err := t.assistant.CountMemberTurnsSince(ctx, assistantstore.CountMemberTurnsSinceParams{MemberID: memberID, Since: since})
	return int(n), err
}

// ListRecentTurns returns the newest turns first.
func (t *postgresTx) ListRecentTurns(ctx context.Context, memberID uuid.UUID, limit int) ([]model.Turn, error) {
	rows, err := t.assistant.ListRecentTurns(ctx, assistantstore.ListRecentTurnsParams{
		MemberID: memberID,
		Lim:      int32(min(limit, 100)), //nolint:gosec // G115: bounded to 100
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Turn, 0, len(rows))
	for _, r := range rows {
		tr, err := mapTurn(r)
		if err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, nil
}

// GetRatingForUpdate locks the member's assistant turn.
func (t *postgresTx) GetRatingForUpdate(ctx context.Context, memberID, messageID uuid.UUID) (*bool, error) {
	row, err := t.assistant.GetAssistantTurnForUpdate(ctx, assistantstore.GetAssistantTurnForUpdateParams{ID: messageID, MemberID: memberID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return row.Helped, nil
}

// SetRating stores the rating (the last one wins).
func (t *postgresTx) SetRating(ctx context.Context, memberID, messageID uuid.UUID, helped bool, at time.Time) error {
	return t.assistant.RateAssistantTurn(ctx, assistantstore.RateAssistantTurnParams{
		Helped: &helped, RatedAt: &at, ID: messageID, MemberID: memberID,
	})
}

// PublishReplyRated enqueues assistant.ReplyRated in this transaction's outbox.
func (t *postgresTx) PublishReplyRated(ctx context.Context, now time.Time, data model.ReplyRatedData) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{
		Type: model.EventReplyRated, Version: model.EventReplyRatedVersion, Data: data,
	})
	return err
}

// PurgeTurnsBefore deletes one bounded batch of expired turns.
func (t *postgresTx) PurgeTurnsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	return t.assistant.PurgeTurnsBefore(ctx, assistantstore.PurgeTurnsBeforeParams{
		Cutoff: cutoff,
		Lim:    int32(min(limit, 100000)), //nolint:gosec // G115: bounded
	})
}

// ScheduleRetentionPurge schedules the singleton purge timer.
func (t *postgresTx) ScheduleRetentionPurge(ctx context.Context, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerRetentionPurge,
		SubjectID: model.RetentionPurgeSubject,
		DedupKey:  model.TimerRetentionPurge,
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
}

// CancelRetentionPurge marks active purge timers done.
func (t *postgresTx) CancelRetentionPurge(ctx context.Context, at time.Time) (int64, error) {
	return timers.Cancel(ctx, t.platform, at, model.TimerRetentionPurge, model.RetentionPurgeSubject)
}

func mapTurn(r assistantstore.AssistantMessage) (model.Turn, error) {
	var sources []model.SourceRef
	if len(r.Sources) > 0 {
		if err := json.Unmarshal(r.Sources, &sources); err != nil {
			return model.Turn{}, fmt.Errorf("decode sources of %s: %w", r.ID, err)
		}
	}
	return model.Turn{
		ID:             r.ID,
		MemberID:       r.MemberID,
		Role:           r.Role,
		ReplyTo:        r.ReplyTo,
		SealedText:     r.TextSealed,
		Crisis:         r.CrisisFlag,
		Degraded:       r.Degraded,
		Sources:        sources,
		Suggest:        r.Suggest,
		EscalationID:   r.EscalationID,
		IdempotencyKey: r.IdempotencyKey,
		Helped:         r.Helped,
		CreatedAt:      r.CreatedAt,
	}, nil
}

var (
	_ port.Store = (*PostgresStore)(nil)
	_ port.Tx    = (*postgresTx)(nil)
)
