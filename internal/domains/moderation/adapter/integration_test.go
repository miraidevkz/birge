package adapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/moderation/adapter"
	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/moderationtest"
	"birge/internal/domains/moderation/port"
	"birge/internal/domains/moderation/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/outbox"
	"birge/internal/platform/testinfra"
)

// fakeContent is fake data that must never reach the database or the logs.
const fakeContent = "FAKE-CONTENT-7f3a Example pram, call +7 701 000 0000"

type env struct {
	pool    *pgxpool.Pool
	svc     *service.Service
	sources map[model.Kind]*moderationtest.FakeSource
	clock   *moderationtest.Clock
	logs    *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	e := &env{
		pool: pool, sources: map[model.Kind]*moderationtest.FakeSource{},
		clock: &moderationtest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		logs:  &syncBuffer{},
	}
	srcs := map[model.Kind]port.ContentSource{}
	for _, k := range model.Kinds {
		e.sources[k] = moderationtest.NewFakeSource()
		srcs[k] = e.sources[k]
	}
	e.svc = &service.Service{
		Store: &adapter.PostgresStore{Pool: pool}, Sources: srcs, Clock: e.clock,
		Logger: slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	return e
}

func moderator() auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"moderator"}, MFA: true}
}

func (e *env) event(t *testing.T, typ string, data any) uuid.UUID {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV7())
	if err := e.svc.HandleEvent(context.Background(), id, typ, raw); err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	return id
}

func (e *env) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) openItem(t *testing.T, kind model.Kind, objectID uuid.UUID) (uuid.UUID, int) {
	t.Helper()
	var id uuid.UUID
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT id, report_count FROM moderation.items WHERE kind = $1 AND object_id = $2 AND status = 'open'`,
		string(kind), objectID).Scan(&id, &n); err != nil {
		t.Fatal(err)
	}
	return id, n
}

func TestItemCreationIsIdempotentPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	listing, spec, review, msg, req := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()

	evListing := e.event(t, model.EventListingSubmitted, model.ListingSubmittedData{ListingID: listing})
	e.event(t, model.EventItemSubmitted, model.ItemSubmittedData{ObjectType: "specialist", ObjectID: spec})
	e.event(t, model.EventItemSubmitted, model.ItemSubmittedData{ObjectType: "review", ObjectID: review})
	e.event(t, model.EventMessageHeld, model.MessageHeldData{MessageID: msg, RoomID: uuid.New(), Reason: "link"})
	e.event(t, model.EventVerificationRequested, model.VerificationRequestedData{MemberID: uuid.New(), RequestID: req})
	if n := e.count(t, `SELECT count(*) FROM moderation.items`); n != 5 {
		t.Fatalf("items = %d, want 5", n)
	}

	// Redelivery of the same event: nothing changes.
	raw, _ := json.Marshal(model.ListingSubmittedData{ListingID: listing})
	if err := e.svc.HandleEvent(ctx, evListing, model.EventListingSubmitted, raw); err != nil {
		t.Fatal(err)
	}
	// A new event for an object that already has an open item: still one item.
	e.event(t, model.EventListingSubmitted, model.ListingSubmittedData{ListingID: listing})
	e.event(t, model.EventMessageHeld, model.MessageHeldData{MessageID: msg, RoomID: uuid.New(), Reason: "phone"})
	if n := e.count(t, `SELECT count(*) FROM moderation.items`); n != 5 {
		t.Fatalf("after duplicates items = %d, want 5", n)
	}
	if n := e.count(t, `SELECT count(*) FROM moderation.items WHERE kind = 'chat_message' AND source_reason = 'link'`); n != 1 {
		t.Fatalf("held message keeps its first reason: %d", n)
	}

	// Concurrent deliveries of different events for one object: one item.
	obj := uuid.New()
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, _ := json.Marshal(model.ListingSubmittedData{ListingID: obj})
			if err := e.svc.HandleEvent(ctx, uuid.Must(uuid.NewV7()), model.EventListingSubmitted, raw); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := e.count(t, `SELECT count(*) FROM moderation.items WHERE object_id = $1`, obj); n != 1 {
		t.Fatalf("concurrent submissions items = %d, want 1", n)
	}

	// After a decision the object may be submitted again (a resubmitted review).
	id, _ := e.openItem(t, model.KindReview, review)
	if err := e.svc.Decide(ctx, moderator(), id, model.Decision{Reason: "inaccurate"}); err != nil {
		t.Fatal(err)
	}
	e.event(t, model.EventItemSubmitted, model.ItemSubmittedData{ObjectType: "review", ObjectID: review})
	if n := e.count(t, `SELECT count(*) FROM moderation.items WHERE object_id = $1`, review); n != 2 {
		t.Fatalf("resubmitted review items = %d, want 2", n)
	}
}

func TestRepeatedReportsBumpCounterPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	msg := uuid.New()
	first := e.event(t, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New(), Reason: "spam"})
	e.event(t, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New(), Reason: "unsafe"})
	e.event(t, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New()})
	// A redelivered report does not count twice.
	raw, _ := json.Marshal(model.MessageReportedData{MessageID: msg, RoomID: uuid.New(), Reason: "spam"})
	if err := e.svc.HandleEvent(ctx, first, model.EventMessageReported, raw); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM moderation.items WHERE object_id = $1`, msg); n != 1 {
		t.Fatalf("report items = %d, want 1", n)
	}
	id, n := e.openItem(t, model.KindReport, msg)
	if n != 3 {
		t.Fatalf("report_count = %d, want 3", n)
	}
	// A held item for the same message is a separate kind.
	e.event(t, model.EventMessageHeld, model.MessageHeldData{MessageID: msg, RoomID: uuid.New(), Reason: "newcomer"})
	if n := e.count(t, `SELECT count(*) FROM moderation.items WHERE object_id = $1`, msg); n != 2 {
		t.Fatalf("report + held items = %d, want 2", n)
	}
	// Once decided, a new report opens a new item.
	if err := e.svc.Decide(ctx, moderator(), id, model.Decision{Approve: true}); err != nil {
		t.Fatal(err)
	}
	e.event(t, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New()})
	if _, n := e.openItem(t, model.KindReport, msg); n != 1 {
		t.Fatalf("new report item count = %d, want 1", n)
	}
}

func TestDecisionWritesEventAndAuditPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	msg := uuid.New()
	e.event(t, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New(), Reason: "unsafe"})
	id, _ := e.openItem(t, model.KindReport, msg)
	staff := auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}, MFA: true}

	if err := e.svc.Decide(ctx, staff, id, model.Decision{Reason: "unsafe", BlockAuthor: true, LegalHold: true}); err != nil {
		t.Fatal(err)
	}
	var payload []byte
	if err := e.pool.QueryRow(ctx,
		`SELECT payload FROM platform.outbox WHERE event_type = $1`, model.EventDecided).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var got model.DecidedData
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatal(err)
	}
	want := model.DecidedData{ItemID: id, Kind: model.KindReport, ObjectID: msg, Reason: "unsafe", BlockAuthor: true, LegalHold: true}
	if got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
	for action, reason := range map[string]string{
		model.AuditActionReject: "unsafe", model.AuditActionBlock: "", model.AuditActionLegalHold: "",
	} {
		if n := e.count(t, `SELECT count(*) FROM platform.audit_log
			WHERE actor_id = $1 AND actor_role = 'admin' AND action = $2 AND object_type = $3
			  AND object_id = $4 AND coalesce(reason, '') = $5`,
			staff.IdentityID, action, model.AuditObjectItem, id, reason); n != 1 {
			t.Errorf("audit %s rows = %d, want 1", action, n)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM moderation.items
		WHERE id = $1 AND status = 'decided' AND decided_by = $2 AND approve = false
		  AND reason = 'unsafe' AND block_author AND legal_hold`, id, staff.IdentityID); n != 1 {
		t.Fatal("item not marked decided")
	}

	// Decided twice: 409, nothing new written.
	err := e.svc.Decide(ctx, moderator(), id, model.Decision{Approve: true})
	if !errors.Is(err, model.ErrAlreadyDecided) {
		t.Fatalf("second decision: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM platform.outbox`); n != 1 {
		t.Fatalf("outbox rows = %d, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM platform.audit_log`); n != 3 {
		t.Fatalf("audit rows = %d, want 3", n)
	}

	// Invalid against the kind: 400, the item stays open.
	listing := uuid.New()
	e.event(t, model.EventListingSubmitted, model.ListingSubmittedData{ListingID: listing})
	lid, _ := e.openItem(t, model.KindListing, listing)
	if err := e.svc.Decide(ctx, moderator(), lid, model.Decision{Reason: "spam", LegalHold: true}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("legal hold on listing: %v", err)
	}
	if err := e.svc.Decide(ctx, moderator(), uuid.New(), model.Decision{Approve: true}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown item: %v", err)
	}
	psy := auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"psychologist"}, MFA: true}
	if err := e.svc.Decide(ctx, psy, lid, model.Decision{Approve: true}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("psychologist: %v", err)
	}
	if _, n := e.openItem(t, model.KindListing, listing); n != 1 {
		t.Fatal("listing item not open")
	}
}

// failingStore wraps the Postgres store and fails Publish, after the item
// update and the audit entries were written in the same transaction.
type failingStore struct{ inner *adapter.PostgresStore }

func (s failingStore) InTx(ctx context.Context, fn func(port.Tx) error) error {
	return s.inner.InTx(ctx, func(tx port.Tx) error { return fn(failingTx{tx}) })
}

type failingTx struct{ port.Tx }

func (failingTx) Publish(context.Context, time.Time, model.Event) error {
	return errors.New("outbox down")
}

func TestDecisionIsAtomicPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	obj := uuid.New()
	e.event(t, model.EventVerificationRequested, model.VerificationRequestedData{MemberID: uuid.New(), RequestID: obj})
	id, _ := e.openItem(t, model.KindVerification, obj)

	broken := *e.svc
	broken.Store = failingStore{inner: &adapter.PostgresStore{Pool: e.pool}}
	if err := broken.Decide(ctx, moderator(), id, model.Decision{Approve: true}); err == nil {
		t.Fatal("want error")
	}
	if n := e.count(t, `SELECT count(*) FROM platform.audit_log`); n != 0 {
		t.Fatalf("audit rows after rollback = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM platform.outbox`); n != 0 {
		t.Fatalf("outbox rows after rollback = %d", n)
	}
	e.openItem(t, model.KindVerification, obj) // still open

	if err := e.svc.Decide(ctx, moderator(), id, model.Decision{Approve: true}); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventDecided); n != 1 {
		t.Fatalf("outbox rows = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM platform.audit_log WHERE action = $1`, model.AuditActionApprove); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
}

func TestConcurrentDecisionsOneWinsPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	msg := uuid.New()
	e.event(t, model.EventMessageHeld, model.MessageHeldData{MessageID: msg, RoomID: uuid.New(), Reason: "link"})
	id, _ := e.openItem(t, model.KindChatMessage, msg)

	const n = 12
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			d := model.Decision{Approve: true}
			if i%2 == 1 {
				d = model.Decision{Reason: "spam", BlockAuthor: true}
			}
			errs[i] = e.svc.Decide(ctx, moderator(), id, d)
		}()
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, model.ErrAlreadyDecided):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want 1", wins)
	}
	if c := e.count(t, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventDecided); c != 1 {
		t.Fatalf("events = %d, want 1", c)
	}
	if c := e.count(t, `SELECT count(*) FROM platform.audit_log WHERE action IN ($1, $2)`,
		model.AuditActionApprove, model.AuditActionReject); c != 1 {
		t.Fatalf("decision audit rows = %d, want 1", c)
	}
}

func TestQueueOrderFilterCursorPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var listings []uuid.UUID
	for i := range model.PageSize + 5 {
		id := uuid.New()
		listings = append(listings, id)
		e.sources[model.KindListing].Put(id, model.Summary{Text: "Example item " + string(rune('A'+i%26)), PhotoURL: "https://media.example.test/p.jpg"})
		e.event(t, model.EventListingSubmitted, model.ListingSubmittedData{ListingID: id})
		e.clock.Advance(time.Second)
	}
	spec := uuid.New()
	e.sources[model.KindSpecialist].Put(spec, model.Summary{Text: "Example Clinic, speech therapist", PhotoURL: "https://media.example.test/x.jpg"})
	e.event(t, model.EventItemSubmitted, model.ItemSubmittedData{ObjectType: "specialist", ObjectID: spec})

	p := moderator()
	page1, err := e.svc.ListQueue(ctx, p, model.KindListing, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != model.PageSize || page1.NextCursor == "" {
		t.Fatalf("page1 = %d items, cursor %q", len(page1.Items), page1.NextCursor)
	}
	page2, err := e.svc.ListQueue(ctx, p, model.KindListing, page1.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 5 || page2.NextCursor != "" {
		t.Fatalf("page2 = %d items, cursor %q", len(page2.Items), page2.NextCursor)
	}
	all := slices.Concat(page1.Items, page2.Items)
	for i, it := range all {
		if it.Item.ObjectID != listings[i] {
			t.Fatalf("position %d: not oldest first", i)
		}
		if it.Summary.Text == "" || it.Summary.PhotoURL == "" {
			t.Fatalf("position %d: summary missing", i)
		}
	}

	specs, err := e.svc.ListQueue(ctx, p, model.KindSpecialist, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(specs.Items) != 1 || specs.Items[0].Item.ObjectID != spec || specs.Items[0].Summary.PhotoURL != "" {
		t.Fatalf("specialist filter: %+v", specs.Items)
	}
	mixed, err := e.svc.ListQueue(ctx, p, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if mixed.Items[0].Item.ObjectID != listings[0] {
		t.Fatal("unfiltered queue not oldest first")
	}

	// Decided items leave the queue.
	if err := e.svc.Decide(ctx, p, all[0].Item.ID, model.Decision{Approve: true}); err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.ListQueue(ctx, p, model.KindListing, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.Items[0].Item.ObjectID != listings[1] {
		t.Fatal("decided item still listed")
	}

	// Gone content: empty summary. Outage: the page fails.
	gone := uuid.New()
	e.event(t, model.EventItemSubmitted, model.ItemSubmittedData{ObjectType: "review", ObjectID: gone})
	reviews, err := e.svc.ListQueue(ctx, p, model.KindReview, "")
	if err != nil || len(reviews.Items) != 1 || reviews.Items[0].Summary.Text != "" {
		t.Fatalf("gone content: %+v %v", reviews.Items, err)
	}
	e.sources[model.KindReview].Fail = errors.New("directory down")
	if _, err := e.svc.ListQueue(ctx, p, model.KindReview, ""); err == nil {
		t.Fatal("want error on source outage")
	}
	if _, err := e.svc.ListQueue(ctx, p, "crisis", ""); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := e.svc.ListQueue(ctx, p, "", "not-a-cursor"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestNoContentInDatabaseOrLogsPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	msg, listing := uuid.New(), uuid.New()
	e.sources[model.KindReport].Put(msg, model.Summary{Text: fakeContent})
	e.sources[model.KindChatMessage].Put(msg, model.Summary{Text: fakeContent})
	e.sources[model.KindListing].Put(listing, model.Summary{Text: fakeContent, PhotoURL: "https://media.example.test/FAKE-CONTENT-7f3a.jpg"})

	e.event(t, model.EventMessageReported, model.MessageReportedData{MessageID: msg, RoomID: uuid.New(), Reason: "Prose " + fakeContent})
	e.event(t, model.EventMessageHeld, model.MessageHeldData{MessageID: msg, RoomID: uuid.New(), Reason: "phone"})
	e.event(t, model.EventListingSubmitted, model.ListingSubmittedData{ListingID: listing})

	p := moderator()
	page, err := e.svc.ListQueue(ctx, p, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %d", len(page.Items))
	}
	for _, it := range page.Items {
		if it.Summary.Text != fakeContent {
			t.Fatalf("summary not shown for %s", it.Item.Kind)
		}
		if err := e.svc.Decide(ctx, p, it.Item.ID, model.Decision{Reason: "unsafe"}); err != nil {
			t.Fatal(err)
		}
	}
	e.sources[model.KindListing].Fail = errors.New("sharing down: " + fakeContent)
	e.event(t, model.EventListingSubmitted, model.ListingSubmittedData{ListingID: uuid.New()})
	_, _ = e.svc.ListQueue(ctx, p, model.KindListing, "")

	if strings.Contains(e.logs.String(), "FAKE-CONTENT") {
		t.Fatal("content found in logs")
	}
	rows, err := e.pool.Query(ctx, `
		SELECT row_to_json(i)::text FROM moderation.items i
		UNION ALL SELECT payload::text FROM platform.outbox
		UNION ALL SELECT row_to_json(a)::text FROM platform.audit_log a`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		seen++
		if strings.Contains(s, "FAKE-CONTENT") || strings.Contains(s, "Prose") {
			t.Fatalf("content stored: %s", s)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatal("no rows checked")
	}
}

func TestQueueConsumerHandlesEnvelopePostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	c := &adapter.QueueConsumer{Service: e.svc}
	listing := uuid.New()
	data, _ := json.Marshal(model.ListingSubmittedData{ListingID: listing})
	body, _ := json.Marshal(outbox.Envelope{
		ID: uuid.Must(uuid.NewV7()), Type: model.EventListingSubmitted, Version: 1,
		OccurredAt: e.clock.Now(), Data: data,
	})
	for range 2 {
		if err := c.HandleMessage(ctx, body); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM moderation.items WHERE object_id = $1`, listing); n != 1 {
		t.Fatalf("items = %d, want 1", n)
	}
	if err := c.HandleMessage(ctx, []byte("garbage")); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("garbage: %v", err)
	}
}
