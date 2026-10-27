// Package port defines the interfaces the circle service needs: its store
// (tea requests, matches, acquaintances, outbox, timers) and what it reads
// from other contexts (member profiles and blocks from membership). Adapters
// for the other contexts are wired later; tests use the fakes in circletest.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/circle/model"
)

// MemberInfo reads member profiles owned by membership. Results are never
// logged, stored or put into events.
type MemberInfo interface {
	// Profiles returns the profiles of the given members; unknown or deleted
	// members are absent from the map.
	Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]model.Profile, error)
}

// Blocks reads member blocks owned by membership (ADR-0019).
type Blocks interface {
	// IsBlocked reports whether a blocked b or b blocked a.
	IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Store is the persistence port for circle tables and platform hooks.
type Store interface {
	// InTx runs fn in one database transaction: committed when fn returns
	// nil, rolled back otherwise.
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction. Every write a use case makes
// (rows, outbox events, timers) goes through the same Tx so they commit or
// roll back together.
type Tx interface {
	// InsertTeaRequest inserts an open request; inserted is false on a
	// repeated idempotency key. A second open request for the member returns
	// model.ErrConflict.
	InsertTeaRequest(ctx context.Context, r model.TeaRequest) (created model.TeaRequest, inserted bool, err error)
	// GetTeaRequestByIdempotencyKey returns the member's request created with key, or model.ErrNotFound.
	GetTeaRequestByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.TeaRequest, error)
	// GetTeaRequest returns the request, or model.ErrNotFound. With forUpdate
	// the row stays locked until the transaction ends.
	GetTeaRequest(ctx context.Context, id uuid.UUID, forUpdate bool) (model.TeaRequest, error)
	// ListTeaRequestsByMember returns the member's latest requests, newest first.
	ListTeaRequestsByMember(ctx context.Context, memberID uuid.UUID, limit int32) ([]model.TeaRequest, error)
	// ListOpenTeaRequests returns non-expired open requests, oldest first.
	ListOpenTeaRequests(ctx context.Context, now time.Time) ([]model.TeaRequest, error)
	// MarkTeaRequestMatched sets status=matched when still open; updated is false otherwise.
	MarkTeaRequestMatched(ctx context.Context, id uuid.UUID) (r model.TeaRequest, updated bool, err error)
	// CancelTeaRequest sets status=cancelled for the owner's open request; updated is false otherwise.
	CancelTeaRequest(ctx context.Context, id, memberID uuid.UUID) (r model.TeaRequest, updated bool, err error)
	// ExpireTeaRequest sets status=expired when still open; updated is false otherwise.
	ExpireTeaRequest(ctx context.Context, id uuid.UUID) (r model.TeaRequest, updated bool, err error)

	// InsertMatch inserts a proposed match.
	InsertMatch(ctx context.Context, m model.Match) (model.Match, error)
	// GetMatch returns the match, or model.ErrNotFound. With forUpdate the row stays locked.
	GetMatch(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Match, error)
	// ListMatchesForMember returns the member's latest matches, newest first.
	ListMatchesForMember(ctx context.Context, memberID uuid.UUID, limit int32) ([]model.Match, error)
	// PairMatchedSince reports whether the canonical pair has a match at or after since.
	PairMatchedSince(ctx context.Context, memberA, memberB uuid.UUID, since time.Time) (bool, error)
	// UpdateMatchAnswers stores answers, status and optional ask_met_at.
	UpdateMatchAnswers(ctx context.Context, id uuid.UUID, answerA, answerB *bool, status string, askMetAt *time.Time) (model.Match, error)
	// UpdateMatchMet stores met answers and status.
	UpdateMatchMet(ctx context.Context, id uuid.UUID, metA, metB *bool, status string) (model.Match, error)
	// SetMatchRoomID stores the chat room id once; updated is false when already set or unknown.
	SetMatchRoomID(ctx context.Context, id, roomID uuid.UUID) (m model.Match, updated bool, err error)
	// SetMatchAskMetAt records ask_met_at once when the AskMet timer fires.
	SetMatchAskMetAt(ctx context.Context, id uuid.UUID, at time.Time) (m model.Match, updated bool, err error)

	// InsertAcquaintance inserts the canonical pair; inserted is false when it already exists.
	InsertAcquaintance(ctx context.Context, memberA, memberB uuid.UUID, since time.Time) (created model.Acquaintance, inserted bool, err error)
	// GetAcquaintance returns the pair, or model.ErrNotFound.
	GetAcquaintance(ctx context.Context, memberA, memberB uuid.UUID) (model.Acquaintance, error)
	// ListAcquaintancesForMember returns the member's acquaintances, newest first.
	ListAcquaintancesForMember(ctx context.Context, memberID uuid.UUID) ([]model.Acquaintance, error)

	// Publish enqueues e in this transaction's outbox (ADR-0007).
	Publish(ctx context.Context, now time.Time, e model.Event) error

	// ScheduleMatchRun schedules circle.MatchRun (dedup = MatchRunSubject); scheduled is false on dedup hit.
	ScheduleMatchRun(ctx context.Context, dueAt time.Time) (scheduled bool, err error)
	// CancelMatchRun cancels the pending MatchRun timer; returns how many were cancelled.
	CancelMatchRun(ctx context.Context, at time.Time) (int64, error)

	// ScheduleRequestExpire schedules circle.RequestExpire for the request (dedup = request id).
	ScheduleRequestExpire(ctx context.Context, requestID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
	// CancelRequestExpire cancels the pending expiry timer of the request.
	CancelRequestExpire(ctx context.Context, requestID uuid.UUID, at time.Time) (int64, error)

	// ScheduleAskMet schedules circle.AskMet for the match (dedup = match id).
	ScheduleAskMet(ctx context.Context, matchID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
	// CancelAskMet cancels the pending AskMet timer of the match.
	CancelAskMet(ctx context.Context, matchID uuid.UUID, at time.Time) (int64, error)
}
