package moderationtest

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/port"
)

// MemStore is an in-memory port.Store for service and handler tests. It
// mirrors db/queries/moderation_items.sql: one open item per kind and
// object, report counters, oldest-first paging by (created_at, id), and a
// decision only on an open item. A transaction is rolled back when fn
// returns an error. The SQL itself is covered by the PostgreSQL tests in
// package adapter.
//
// FailOn makes one Tx method return an error, for failure-path tests.
type MemStore struct {
	mu    sync.Mutex
	state memState
	fail  map[string]error
}

type memState struct {
	items     map[uuid.UUID]model.Item
	processed map[string]bool
	events    []model.Event
	audit     []port.AuditEntry
}

func (s memState) clone() memState {
	out := memState{
		items: make(map[uuid.UUID]model.Item, len(s.items)), processed: make(map[string]bool, len(s.processed)),
		events: slices.Clone(s.events), audit: slices.Clone(s.audit),
	}
	for k, v := range s.items {
		out.items[k] = v
	}
	for k, v := range s.processed {
		out.processed[k] = v
	}
	return out
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{state: memState{}.clone(), fail: map[string]error{}}
}

// FailOn makes the Tx method named method return err; a nil err clears it.
// "InTx" fails the transaction itself.
func (s *MemStore) FailOn(method string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		delete(s.fail, method)
		return
	}
	s.fail[method] = err
}

// InTx implements port.Store.
func (s *MemStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("memstore: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.fail["InTx"]; err != nil {
		return err
	}
	saved := s.state.clone()
	if err := fn(&memTx{s: s}); err != nil {
		s.state = saved
		return err
	}
	return nil
}

// Item returns a committed item.
func (s *MemStore) Item(id uuid.UUID) (model.Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.state.items[id]
	return it, ok
}

// Items returns every committed item, oldest first.
func (s *MemStore) Items() []model.Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Item, 0, len(s.state.items))
	for _, it := range s.state.items {
		out = append(out, it)
	}
	sortItems(out)
	return out
}

// Events returns the committed outbox events.
func (s *MemStore) Events() []model.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.state.events)
}

// Audit returns the committed audit entries.
func (s *MemStore) Audit() []port.AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.state.audit)
}

func sortItems(items []model.Item) {
	sort.Slice(items, func(i, j int) bool { return before(items[i].CreatedAt, items[i].ID, items[j].CreatedAt, items[j].ID) })
}

// before orders by (created_at, id) like the SQL row comparison.
func before(at time.Time, id uuid.UUID, at2 time.Time, id2 uuid.UUID) bool {
	if !at.Equal(at2) {
		return at.Before(at2)
	}
	return strings.Compare(id.String(), id2.String()) < 0
}

type memTx struct{ s *MemStore }

var _ port.Tx = (*memTx)(nil)

func (t *memTx) st() *memState { return &t.s.state }

func (t *memTx) openItem(kind model.Kind, objectID uuid.UUID) (model.Item, bool) {
	for _, it := range t.st().items {
		if it.Kind == kind && it.ObjectID == objectID && it.Status == model.StatusOpen {
			return it, true
		}
	}
	return model.Item{}, false
}

// MarkProcessed implements port.Tx.
func (t *memTx) MarkProcessed(_ context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	if err := t.s.fail["MarkProcessed"]; err != nil {
		return false, err
	}
	k := consumer + "|" + eventID.String()
	if t.st().processed[k] {
		return false, nil
	}
	t.st().processed[k] = true
	return true, nil
}

// InsertItem implements port.Tx.
func (t *memTx) InsertItem(_ context.Context, it model.Item) (bool, error) {
	if err := t.s.fail["InsertItem"]; err != nil {
		return false, err
	}
	if _, ok := t.openItem(it.Kind, it.ObjectID); ok {
		return false, nil
	}
	it.Status, it.ReportCount = model.StatusOpen, 1
	t.st().items[it.ID] = it
	return true, nil
}

// UpsertReportItem implements port.Tx.
func (t *memTx) UpsertReportItem(_ context.Context, it model.Item) (uuid.UUID, int, error) {
	if err := t.s.fail["UpsertReportItem"]; err != nil {
		return uuid.Nil, 0, err
	}
	if open, ok := t.openItem(model.KindReport, it.ObjectID); ok {
		open.ReportCount++
		t.st().items[open.ID] = open
		return open.ID, open.ReportCount, nil
	}
	it.Kind, it.Status, it.ReportCount = model.KindReport, model.StatusOpen, 1
	t.st().items[it.ID] = it
	return it.ID, 1, nil
}

// GetItemForUpdate implements port.Tx.
func (t *memTx) GetItemForUpdate(_ context.Context, id uuid.UUID) (model.Item, error) {
	if err := t.s.fail["GetItemForUpdate"]; err != nil {
		return model.Item{}, err
	}
	it, ok := t.st().items[id]
	if !ok {
		return model.Item{}, model.ErrNotFound
	}
	return it, nil
}

// DecideItem implements port.Tx.
func (t *memTx) DecideItem(_ context.Context, id, by uuid.UUID, at time.Time, d model.Decision) (bool, error) {
	if err := t.s.fail["DecideItem"]; err != nil {
		return false, err
	}
	it, ok := t.st().items[id]
	if !ok || it.Status != model.StatusOpen {
		return false, nil
	}
	it.Status, it.DecidedBy, it.DecidedAt, it.Decision = model.StatusDecided, by, at, d
	t.st().items[id] = it
	return true, nil
}

// ListOpenItems implements port.Tx.
func (t *memTx) ListOpenItems(_ context.Context, f port.ListFilter) ([]model.Item, error) {
	if err := t.s.fail["ListOpenItems"]; err != nil {
		return nil, err
	}
	var out []model.Item
	for _, it := range t.st().items {
		if it.Status != model.StatusOpen || (f.Kind != "" && it.Kind != f.Kind) {
			continue
		}
		if !f.CursorCreatedAt.IsZero() && !before(f.CursorCreatedAt, f.CursorID, it.CreatedAt, it.ID) {
			continue
		}
		out = append(out, it)
	}
	sortItems(out)
	if len(out) > int(f.Limit) {
		out = out[:f.Limit]
	}
	return out, nil
}

// Publish implements port.Tx.
func (t *memTx) Publish(_ context.Context, _ time.Time, e model.Event) error {
	if err := t.s.fail["Publish"]; err != nil {
		return err
	}
	t.st().events = append(t.st().events, e)
	return nil
}

// Audit implements port.Tx.
func (t *memTx) Audit(_ context.Context, e port.AuditEntry) error {
	if err := t.s.fail["Audit"]; err != nil {
		return err
	}
	t.st().audit = append(t.st().audit, e)
	return nil
}
