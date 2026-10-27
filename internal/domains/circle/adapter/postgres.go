// Package adapter implements the circle ports on PostgreSQL (schema circle,
// sqlc in ../store) and the platform hooks it needs: outbox events, the
// MatchRun / RequestExpire / AskMet timers, and the timer handlers. Ports
// onto other contexts (membership profiles and blocks) are wired later.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/port"
	circlestore "birge/internal/domains/circle/store"
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
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction, such as the
// one the timer worker passes to a handler.
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{q: circlestore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	q        *circlestore.Queries
	platform *platformstore.Queries
}

func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// InsertTeaRequest inserts an open request; inserted is false on idempotency replay.
func (t *postgresTx) InsertTeaRequest(ctx context.Context, r model.TeaRequest) (model.TeaRequest, bool, error) {
	row, err := t.q.InsertTeaRequest(ctx, circlestore.InsertTeaRequestParams{
		ID: r.ID, MemberID: r.MemberID, IdempotencyKey: r.IdempotencyKey, Format: r.Format,
		SameDistrict: r.Preferences.SameDistrict, SimilarAge: r.Preferences.SimilarAge,
		SharedNeeds: r.Preferences.SharedNeeds, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.TeaRequest{}, false, nil
	}
	if isUniqueViolation(err) {
		return model.TeaRequest{}, false, model.ErrConflict
	}
	if err != nil {
		return model.TeaRequest{}, false, fmt.Errorf("insert tea request: %w", err)
	}
	return mapTea(row), true, nil
}

// GetTeaRequestByIdempotencyKey returns the member's request created with key.
func (t *postgresTx) GetTeaRequestByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.TeaRequest, error) {
	row, err := t.q.GetTeaRequestByIdempotencyKey(ctx, circlestore.GetTeaRequestByIdempotencyKeyParams{
		MemberID: memberID, IdempotencyKey: key,
	})
	if err != nil {
		return model.TeaRequest{}, notFound(err, "get tea by key")
	}
	return mapTea(row), nil
}

// GetTeaRequest returns the request, optionally locked.
func (t *postgresTx) GetTeaRequest(ctx context.Context, id uuid.UUID, forUpdate bool) (model.TeaRequest, error) {
	var row circlestore.CircleTeaRequest
	var err error
	if forUpdate {
		row, err = t.q.GetTeaRequestForUpdate(ctx, id)
	} else {
		row, err = t.q.GetTeaRequest(ctx, id)
	}
	if err != nil {
		return model.TeaRequest{}, notFound(err, "get tea")
	}
	return mapTea(row), nil
}

// ListTeaRequestsByMember returns the member's latest requests.
func (t *postgresTx) ListTeaRequestsByMember(ctx context.Context, memberID uuid.UUID, limit int32) ([]model.TeaRequest, error) {
	rows, err := t.q.ListTeaRequestsByMember(ctx, circlestore.ListTeaRequestsByMemberParams{
		MemberID: memberID, PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list tea requests: %w", err)
	}
	out := make([]model.TeaRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapTea(r))
	}
	return out, nil
}

// ListOpenTeaRequests returns non-expired open requests, oldest first.
func (t *postgresTx) ListOpenTeaRequests(ctx context.Context, now time.Time) ([]model.TeaRequest, error) {
	rows, err := t.q.ListOpenTeaRequests(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("list open tea: %w", err)
	}
	out := make([]model.TeaRequest, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapTea(r))
	}
	return out, nil
}

// MarkTeaRequestMatched sets status=matched when still open.
func (t *postgresTx) MarkTeaRequestMatched(ctx context.Context, id uuid.UUID) (model.TeaRequest, bool, error) {
	row, err := t.q.MarkTeaRequestMatched(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.TeaRequest{}, false, nil
	}
	if err != nil {
		return model.TeaRequest{}, false, fmt.Errorf("mark tea matched: %w", err)
	}
	return mapTea(row), true, nil
}

// CancelTeaRequest cancels the owner's open request.
func (t *postgresTx) CancelTeaRequest(ctx context.Context, id, memberID uuid.UUID) (model.TeaRequest, bool, error) {
	row, err := t.q.CancelTeaRequest(ctx, circlestore.CancelTeaRequestParams{ID: id, MemberID: memberID})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.TeaRequest{}, false, nil
	}
	if err != nil {
		return model.TeaRequest{}, false, fmt.Errorf("cancel tea: %w", err)
	}
	return mapTea(row), true, nil
}

// ExpireTeaRequest expires an open request.
func (t *postgresTx) ExpireTeaRequest(ctx context.Context, id uuid.UUID) (model.TeaRequest, bool, error) {
	row, err := t.q.ExpireTeaRequest(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.TeaRequest{}, false, nil
	}
	if err != nil {
		return model.TeaRequest{}, false, fmt.Errorf("expire tea: %w", err)
	}
	return mapTea(row), true, nil
}

// InsertMatch inserts a proposed match.
func (t *postgresTx) InsertMatch(ctx context.Context, m model.Match) (model.Match, error) {
	row, err := t.q.InsertMatch(ctx, circlestore.InsertMatchParams{
		ID: m.ID, MemberA: m.MemberA, MemberB: m.MemberB,
		RequestAID: m.RequestAID, RequestBID: m.RequestBID, Format: m.Format, CreatedAt: m.CreatedAt,
	})
	if err != nil {
		return model.Match{}, fmt.Errorf("insert match: %w", err)
	}
	return mapMatch(row), nil
}

// GetMatch returns the match, optionally locked.
func (t *postgresTx) GetMatch(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Match, error) {
	var row circlestore.CircleMatch
	var err error
	if forUpdate {
		row, err = t.q.GetMatchForUpdate(ctx, id)
	} else {
		row, err = t.q.GetMatch(ctx, id)
	}
	if err != nil {
		return model.Match{}, notFound(err, "get match")
	}
	return mapMatch(row), nil
}

// ListMatchesForMember returns the member's latest matches.
func (t *postgresTx) ListMatchesForMember(ctx context.Context, memberID uuid.UUID, limit int32) ([]model.Match, error) {
	rows, err := t.q.ListMatchesForMember(ctx, circlestore.ListMatchesForMemberParams{
		MemberID: memberID, PageLimit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list matches: %w", err)
	}
	out := make([]model.Match, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapMatch(r))
	}
	return out, nil
}

// PairMatchedSince reports whether the pair has a match at or after since.
func (t *postgresTx) PairMatchedSince(ctx context.Context, memberA, memberB uuid.UUID, since time.Time) (bool, error) {
	v, err := t.q.PairMatchedSince(ctx, circlestore.PairMatchedSinceParams{
		MemberA: memberA, MemberB: memberB, Since: since,
	})
	if err != nil {
		return false, fmt.Errorf("pair matched since: %w", err)
	}
	return v, nil
}

// UpdateMatchAnswers stores answers, status and optional ask_met_at.
func (t *postgresTx) UpdateMatchAnswers(ctx context.Context, id uuid.UUID, answerA, answerB *bool, status string, askMetAt *time.Time) (model.Match, error) {
	row, err := t.q.UpdateMatchAnswers(ctx, circlestore.UpdateMatchAnswersParams{
		AnswerA: answerA, AnswerB: answerB, Status: status, AskMetAt: askMetAt, ID: id,
	})
	if err != nil {
		return model.Match{}, fmt.Errorf("update match answers: %w", err)
	}
	return mapMatch(row), nil
}

// UpdateMatchMet stores met answers and status.
func (t *postgresTx) UpdateMatchMet(ctx context.Context, id uuid.UUID, metA, metB *bool, status string) (model.Match, error) {
	row, err := t.q.UpdateMatchMet(ctx, circlestore.UpdateMatchMetParams{
		MetA: metA, MetB: metB, Status: status, ID: id,
	})
	if err != nil {
		return model.Match{}, fmt.Errorf("update match met: %w", err)
	}
	return mapMatch(row), nil
}

// SetMatchRoomID stores the chat room id once.
func (t *postgresTx) SetMatchRoomID(ctx context.Context, id, roomID uuid.UUID) (model.Match, bool, error) {
	rid := roomID
	row, err := t.q.SetMatchRoomID(ctx, circlestore.SetMatchRoomIDParams{RoomID: &rid, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Match{}, false, nil
	}
	if err != nil {
		return model.Match{}, false, fmt.Errorf("set match room: %w", err)
	}
	return mapMatch(row), true, nil
}

// SetMatchAskMetAt records ask_met_at once.
func (t *postgresTx) SetMatchAskMetAt(ctx context.Context, id uuid.UUID, at time.Time) (model.Match, bool, error) {
	row, err := t.q.SetMatchAskMetAt(ctx, circlestore.SetMatchAskMetAtParams{AskMetAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Match{}, false, nil
	}
	if err != nil {
		return model.Match{}, false, fmt.Errorf("set ask met at: %w", err)
	}
	return mapMatch(row), true, nil
}

// InsertAcquaintance inserts the canonical pair; inserted is false when it exists.
func (t *postgresTx) InsertAcquaintance(ctx context.Context, memberA, memberB uuid.UUID, since time.Time) (model.Acquaintance, bool, error) {
	row, err := t.q.InsertAcquaintance(ctx, circlestore.InsertAcquaintanceParams{
		MemberA: memberA, MemberB: memberB, Since: since,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := t.q.GetAcquaintance(ctx, circlestore.GetAcquaintanceParams{MemberA: memberA, MemberB: memberB})
		if err != nil {
			return model.Acquaintance{}, false, fmt.Errorf("get acquaintance after conflict: %w", err)
		}
		return mapAcq(existing), false, nil
	}
	if err != nil {
		return model.Acquaintance{}, false, fmt.Errorf("insert acquaintance: %w", err)
	}
	return mapAcq(row), true, nil
}

// GetAcquaintance returns the pair, or model.ErrNotFound.
func (t *postgresTx) GetAcquaintance(ctx context.Context, memberA, memberB uuid.UUID) (model.Acquaintance, error) {
	row, err := t.q.GetAcquaintance(ctx, circlestore.GetAcquaintanceParams{MemberA: memberA, MemberB: memberB})
	if err != nil {
		return model.Acquaintance{}, notFound(err, "get acquaintance")
	}
	return mapAcq(row), nil
}

// ListAcquaintancesForMember returns the member's acquaintances.
func (t *postgresTx) ListAcquaintancesForMember(ctx context.Context, memberID uuid.UUID) ([]model.Acquaintance, error) {
	rows, err := t.q.ListAcquaintancesForMember(ctx, memberID)
	if err != nil {
		return nil, fmt.Errorf("list acquaintances: %w", err)
	}
	out := make([]model.Acquaintance, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapAcq(r))
	}
	return out, nil
}

// Publish enqueues e in this transaction's outbox.
func (t *postgresTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{Type: e.Type, Version: e.Version, Data: e.Data})
	return err
}

// ScheduleMatchRun schedules circle.MatchRun.
func (t *postgresTx) ScheduleMatchRun(ctx context.Context, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerMatchRun, SubjectID: model.MatchRunSubject,
		DedupKey: model.MatchRunSubject.String(), DueAt: dueAt, Payload: map[string]string{},
	})
}

// CancelMatchRun cancels the pending MatchRun timer.
func (t *postgresTx) CancelMatchRun(ctx context.Context, at time.Time) (int64, error) {
	return timers.Cancel(ctx, t.platform, at, model.TimerMatchRun, model.MatchRunSubject)
}

// ScheduleRequestExpire schedules circle.RequestExpire for the request.
func (t *postgresTx) ScheduleRequestExpire(ctx context.Context, requestID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerRequestExpire, SubjectID: requestID,
		DedupKey: requestID.String(), DueAt: dueAt, Payload: map[string]string{},
	})
}

// CancelRequestExpire cancels the pending expiry timer of the request.
func (t *postgresTx) CancelRequestExpire(ctx context.Context, requestID uuid.UUID, at time.Time) (int64, error) {
	return timers.Cancel(ctx, t.platform, at, model.TimerRequestExpire, requestID)
}

// ScheduleAskMet schedules circle.AskMet for the match.
func (t *postgresTx) ScheduleAskMet(ctx context.Context, matchID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind: model.TimerAskMet, SubjectID: matchID,
		DedupKey: matchID.String(), DueAt: dueAt, Payload: map[string]string{},
	})
}

// CancelAskMet cancels the pending AskMet timer of the match.
func (t *postgresTx) CancelAskMet(ctx context.Context, matchID uuid.UUID, at time.Time) (int64, error) {
	return timers.Cancel(ctx, t.platform, at, model.TimerAskMet, matchID)
}

func mapTea(r circlestore.CircleTeaRequest) model.TeaRequest {
	return model.TeaRequest{
		ID: r.ID, MemberID: r.MemberID, IdempotencyKey: r.IdempotencyKey, Format: r.Format,
		Preferences: model.Preferences{SameDistrict: r.SameDistrict, SimilarAge: r.SimilarAge, SharedNeeds: r.SharedNeeds},
		Status:      r.Status, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	}
}

func mapMatch(r circlestore.CircleMatch) model.Match {
	return model.Match{
		ID: r.ID, MemberA: r.MemberA, MemberB: r.MemberB, RequestAID: r.RequestAID, RequestBID: r.RequestBID,
		Format: r.Format, AnswerA: r.AnswerA, AnswerB: r.AnswerB, MetA: r.MetA, MetB: r.MetB,
		Status: r.Status, RoomID: r.RoomID, AskMetAt: r.AskMetAt, CreatedAt: r.CreatedAt,
	}
}

func mapAcq(r circlestore.CircleAcquaintance) model.Acquaintance {
	return model.Acquaintance{MemberA: r.MemberA, MemberB: r.MemberB, Since: r.Since}
}

var _ port.Store = (*PostgresStore)(nil)
var _ port.Tx = (*postgresTx)(nil)
