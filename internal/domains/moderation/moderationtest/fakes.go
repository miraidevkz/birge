// Package moderationtest holds test doubles for the moderation ports: a fixed
// clock, an in-memory ContentSource and an in-memory store with transaction
// rollback and failure injection (MemStore). Content is fake data only.
package moderationtest

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
)

// Clock is a test clock; Advance moves it forward.
type Clock struct {
	mu sync.Mutex
	T  time.Time
}

// Now implements clock.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.T
}

// Advance moves the clock by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.T = c.T.Add(d)
}

// FakeSource is an in-memory port.ContentSource. Unknown objects return
// model.ErrNotFound; Fail, when set, is returned for every call.
type FakeSource struct {
	mu        sync.Mutex
	summaries map[uuid.UUID]model.Summary
	Fail      error
}

// NewFakeSource returns an empty FakeSource.
func NewFakeSource() *FakeSource {
	return &FakeSource{summaries: map[uuid.UUID]model.Summary{}}
}

// Put stores the summary of an object.
func (f *FakeSource) Put(id uuid.UUID, s model.Summary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.summaries[id] = s
}

// Summary implements port.ContentSource.
func (f *FakeSource) Summary(_ context.Context, id uuid.UUID) (model.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return model.Summary{}, f.Fail
	}
	s, ok := f.summaries[id]
	if !ok {
		return model.Summary{}, model.ErrNotFound
	}
	return s, nil
}
