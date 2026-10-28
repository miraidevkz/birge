// Package assistanttest holds in-memory fakes of the assistant ports for
// unit tests, including a fake Retriever until the knowledge context
// implements the real one.
package assistanttest

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/model"
	"birge/internal/domains/assistant/port"
)

// FakeSealer binds the "ciphertext" to its associated data, so a turn
// opened with the wrong row ID fails like the real envelope.
type FakeSealer struct {
	SealErr error
	OpenErr error
}

// Seal implements port.Sealer.
func (f *FakeSealer) Seal(_ context.Context, memberID uuid.UUID, plaintext, ad []byte) ([]byte, error) {
	if f.SealErr != nil {
		return nil, f.SealErr
	}
	out := []byte("sealed|" + memberID.String() + "|" + string(ad) + "|")
	return append(out, plaintext...), nil
}

// Open implements port.Sealer.
func (f *FakeSealer) Open(_ context.Context, memberID uuid.UUID, ciphertext, ad []byte) ([]byte, error) {
	if f.OpenErr != nil {
		return nil, f.OpenErr
	}
	prefix := []byte("sealed|" + memberID.String() + "|" + string(ad) + "|")
	if !bytes.HasPrefix(ciphertext, prefix) {
		return nil, errors.New("fake sealer: associated data mismatch")
	}
	return bytes.Clone(ciphertext[len(prefix):]), nil
}

// FakeConsent answers HasConsent from Granted.
type FakeConsent struct {
	Granted bool
	Err     error
	Calls   int
}

// HasConsent implements port.ConsentChecker.
func (f *FakeConsent) HasConsent(_ context.Context, _ uuid.UUID, consentType string) (bool, error) {
	f.Calls++
	if consentType != model.ConsentAICrossborder {
		return false, errors.New("fake consent: unexpected type")
	}
	return f.Granted, f.Err
}

// FakeDetector fires when the text contains any of Phrases (case-insensitive).
type FakeDetector struct {
	Phrases []string
	Calls   int
}

// Detect implements port.CrisisDetector.
func (f *FakeDetector) Detect(text, _ string) bool {
	f.Calls++
	lower := strings.ToLower(text)
	for _, p := range f.Phrases {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// FakeOpener records escalations.
type FakeOpener struct {
	NextID uuid.UUID
	Err    error
	Calls  []string // sources
}

// OpenEscalation implements port.CrisisOpener.
func (f *FakeOpener) OpenEscalation(_ context.Context, _ uuid.UUID, source string) (uuid.UUID, error) {
	f.Calls = append(f.Calls, source)
	if f.Err != nil {
		return uuid.Nil, f.Err
	}
	return f.NextID, nil
}

// FakeProfiles returns Value or Err.
type FakeProfiles struct {
	Value model.Profile
	Err   error
}

// Profile implements port.Profiles.
func (f *FakeProfiles) Profile(context.Context, uuid.UUID) (model.Profile, error) {
	return f.Value, f.Err
}

// FakeRetriever returns Fragments (at most k) or Err.
type FakeRetriever struct {
	Fragments []model.Fragment
	Err       error
	Queries   []string // anonymized texts it was asked for
}

// Search implements port.Retriever.
func (f *FakeRetriever) Search(_ context.Context, _, _, text string, k int) ([]model.Fragment, error) {
	f.Queries = append(f.Queries, text)
	if f.Err != nil {
		return nil, f.Err
	}
	if len(f.Fragments) > k {
		return f.Fragments[:k], nil
	}
	return f.Fragments, nil
}

// FakeLLM returns JSON or Err and records every request.
type FakeLLM struct {
	JSON     string
	Err      error
	Requests []model.LLMRequest
}

// Complete implements port.LLM.
func (f *FakeLLM) Complete(_ context.Context, req model.LLMRequest) (model.Completion, error) {
	f.Requests = append(f.Requests, req)
	if f.Err != nil {
		return model.Completion{ModelID: "fake-model"}, f.Err
	}
	return model.Completion{JSON: f.JSON, ModelID: "fake-model"}, nil
}

// MemStore is an in-memory port.Store. A failed transaction rolls back.
type MemStore struct {
	mu       sync.Mutex
	Turns    []model.Turn
	Events   []model.ReplyRatedData
	Answered []model.QuestionAnsweredData // assistant.QuestionAnswered events
	Timers   []Timer
	Err      error // returned by InTx when set
	// AnsweredErr fails PublishQuestionAnswered. Like the savepoint in the
	// Postgres adapter, the failure writes nothing and leaves the
	// transaction usable.
	AnsweredErr error
}

// Timer is a recorded retention timer.
type Timer struct {
	DueAt  time.Time
	DoneAt *time.Time
}

// InTx implements port.Store.
func (s *MemStore) InTx(_ context.Context, fn func(tx port.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return s.Err
	}
	tx := &memTx{
		turns:       append([]model.Turn(nil), s.Turns...),
		events:      append([]model.ReplyRatedData(nil), s.Events...),
		answered:    append([]model.QuestionAnsweredData(nil), s.Answered...),
		timers:      append([]Timer(nil), s.Timers...),
		answeredErr: s.AnsweredErr,
	}
	if err := fn(tx); err != nil {
		return err
	}
	s.Turns, s.Events, s.Answered, s.Timers = tx.turns, tx.events, tx.answered, tx.timers
	return nil
}

// AnsweredSnapshot returns a copy of the committed QuestionAnswered events.
func (s *MemStore) AnsweredSnapshot() []model.QuestionAnsweredData {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.QuestionAnsweredData(nil), s.Answered...)
}

// TurnsSnapshot returns a copy of the stored turns.
func (s *MemStore) TurnsSnapshot() []model.Turn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]model.Turn(nil), s.Turns...)
}

type memTx struct {
	turns       []model.Turn
	events      []model.ReplyRatedData
	answered    []model.QuestionAnsweredData
	timers      []Timer
	answeredErr error
}

// InsertExchange implements port.Tx.
func (t *memTx) InsertExchange(_ context.Context, member, assistant model.Turn) (bool, error) {
	for _, x := range t.turns {
		if x.MemberID == member.MemberID && x.IdempotencyKey != nil && member.IdempotencyKey != nil && *x.IdempotencyKey == *member.IdempotencyKey {
			return false, nil
		}
	}
	t.turns = append(t.turns, member, assistant)
	return true, nil
}

// GetExchangeByIdempotencyKey implements port.Tx.
func (t *memTx) GetExchangeByIdempotencyKey(_ context.Context, memberID, key uuid.UUID) (model.Turn, model.Turn, error) {
	for _, m := range t.turns {
		if m.MemberID != memberID || m.IdempotencyKey == nil || *m.IdempotencyKey != key {
			continue
		}
		for _, a := range t.turns {
			if a.ReplyTo != nil && *a.ReplyTo == m.ID {
				return m, a, nil
			}
		}
	}
	return model.Turn{}, model.Turn{}, model.ErrNotFound
}

// CountMemberTurnsSince implements port.Tx.
func (t *memTx) CountMemberTurnsSince(_ context.Context, memberID uuid.UUID, since time.Time) (int, error) {
	n := 0
	for _, x := range t.turns {
		if x.MemberID == memberID && x.Role == model.RoleMember && !x.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

// ListRecentTurns implements port.Tx.
func (t *memTx) ListRecentTurns(_ context.Context, memberID uuid.UUID, limit int) ([]model.Turn, error) {
	var mine []model.Turn
	for _, x := range t.turns {
		if x.MemberID == memberID {
			mine = append(mine, x)
		}
	}
	sort.SliceStable(mine, func(i, j int) bool {
		if !mine[i].CreatedAt.Equal(mine[j].CreatedAt) {
			return mine[i].CreatedAt.After(mine[j].CreatedAt)
		}
		return mine[i].ID.String() > mine[j].ID.String()
	})
	if len(mine) > limit {
		mine = mine[:limit]
	}
	return mine, nil
}

// GetRatingForUpdate implements port.Tx.
func (t *memTx) GetRatingForUpdate(_ context.Context, memberID, messageID uuid.UUID) (*bool, error) {
	for _, x := range t.turns {
		if x.ID == messageID && x.MemberID == memberID && x.Role == model.RoleAssistant {
			return x.Helped, nil
		}
	}
	return nil, model.ErrNotFound
}

// SetRating implements port.Tx.
func (t *memTx) SetRating(_ context.Context, memberID, messageID uuid.UUID, helped bool, _ time.Time) error {
	for i, x := range t.turns {
		if x.ID == messageID && x.MemberID == memberID && x.Role == model.RoleAssistant {
			h := helped
			t.turns[i].Helped = &h
			return nil
		}
	}
	return model.ErrNotFound
}

// PublishReplyRated implements port.Tx.
func (t *memTx) PublishReplyRated(_ context.Context, _ time.Time, data model.ReplyRatedData) error {
	t.events = append(t.events, data)
	return nil
}

// PublishQuestionAnswered implements port.Tx.
func (t *memTx) PublishQuestionAnswered(_ context.Context, _ time.Time, data model.QuestionAnsweredData) error {
	if t.answeredErr != nil {
		return t.answeredErr
	}
	t.answered = append(t.answered, data)
	return nil
}

// PurgeTurnsBefore implements port.Tx.
func (t *memTx) PurgeTurnsBefore(_ context.Context, cutoff time.Time, limit int) (int64, error) {
	var keep []model.Turn
	var n int64
	for _, x := range t.turns {
		if x.CreatedAt.Before(cutoff) && n < int64(limit) {
			n++
			continue
		}
		keep = append(keep, x)
	}
	t.turns = keep
	return n, nil
}

// ScheduleRetentionPurge implements port.Tx.
func (t *memTx) ScheduleRetentionPurge(_ context.Context, dueAt time.Time) (bool, error) {
	for _, x := range t.timers {
		if x.DoneAt == nil {
			return false, nil
		}
	}
	t.timers = append(t.timers, Timer{DueAt: dueAt})
	return true, nil
}

// CancelRetentionPurge implements port.Tx.
func (t *memTx) CancelRetentionPurge(_ context.Context, at time.Time) (int64, error) {
	var n int64
	for i := range t.timers {
		if t.timers[i].DoneAt == nil {
			done := at
			t.timers[i].DoneAt = &done
			n++
		}
	}
	return n, nil
}

var (
	_ port.Sealer         = (*FakeSealer)(nil)
	_ port.ConsentChecker = (*FakeConsent)(nil)
	_ port.CrisisDetector = (*FakeDetector)(nil)
	_ port.CrisisOpener   = (*FakeOpener)(nil)
	_ port.Profiles       = (*FakeProfiles)(nil)
	_ port.Retriever      = (*FakeRetriever)(nil)
	_ port.LLM            = (*FakeLLM)(nil)
	_ port.Store          = (*MemStore)(nil)
	_ port.Tx             = (*memTx)(nil)
)
