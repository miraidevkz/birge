package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/moderationtest"
	"birge/internal/domains/moderation/port"
	"birge/internal/domains/moderation/service"
	"birge/internal/platform/auth"
)

// fakeContent is fake data that must never reach the logs, the store or an event.
const fakeContent = "FAKE-CONTENT-7f3a Example pram"

type env struct {
	t       *testing.T
	ctx     context.Context
	store   *moderationtest.MemStore
	sources map[model.Kind]*moderationtest.FakeSource
	clock   *moderationtest.Clock
	svc     *service.Service
	logs    *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		t: t, ctx: context.Background(), store: moderationtest.NewMemStore(),
		sources: map[model.Kind]*moderationtest.FakeSource{},
		clock:   &moderationtest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		logs:    &bytes.Buffer{},
	}
	srcs := map[model.Kind]port.ContentSource{}
	for _, k := range model.Kinds {
		e.sources[k] = moderationtest.NewFakeSource()
		srcs[k] = e.sources[k]
	}
	e.svc = &service.Service{Store: e.store, Sources: srcs, Clock: e.clock, Logger: slog.New(slog.NewJSONHandler(e.logs, nil))}
	t.Cleanup(func() {
		if strings.Contains(e.logs.String(), "FAKE-CONTENT") {
			t.Error("content in logs")
		}
	})
	return e
}

func mod(roles ...string) auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: roles, MFA: true}
}

// submit opens an item through the event path and returns its id.
func (e *env) submit(kind model.Kind, object uuid.UUID, reason string) uuid.UUID {
	e.t.Helper()
	if err := e.svc.Submit(e.ctx, uuid.Must(uuid.NewV7()), model.Submission{Kind: kind, ObjectID: object, SourceReason: reason}); err != nil {
		e.t.Fatal(err)
	}
	for _, it := range e.store.Items() {
		if it.Kind == kind && it.ObjectID == object && it.Status == model.StatusOpen {
			return it.ID
		}
	}
	e.t.Fatal("no open item")
	return uuid.Nil
}

func TestHandleEventOpensOneItemPerKind(t *testing.T) {
	e := newEnv(t)
	listing, specialist, review, held, reported, request := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	raw := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	events := []struct {
		typ    string
		data   json.RawMessage
		kind   model.Kind
		object uuid.UUID
		reason string
	}{
		{model.EventListingSubmitted, raw(model.ListingSubmittedData{ListingID: listing}), model.KindListing, listing, ""},
		{model.EventItemSubmitted, raw(model.ItemSubmittedData{ObjectType: "specialist", ObjectID: specialist}), model.KindSpecialist, specialist, ""},
		{model.EventItemSubmitted, raw(model.ItemSubmittedData{ObjectType: "review", ObjectID: review}), model.KindReview, review, ""},
		{model.EventMessageHeld, raw(model.MessageHeldData{MessageID: held, RoomID: uuid.New(), Reason: "link"}), model.KindChatMessage, held, "link"},
		{model.EventMessageReported, raw(model.MessageReportedData{MessageID: reported, RoomID: uuid.New(), Reason: "harassment"}), model.KindReport, reported, "harassment"},
		{model.EventVerificationRequested, raw(model.VerificationRequestedData{MemberID: uuid.New(), RequestID: request}), model.KindVerification, request, ""},
	}
	for _, ev := range events {
		id := uuid.Must(uuid.NewV7())
		for range 2 { // redelivery
			if err := e.svc.HandleEvent(e.ctx, id, ev.typ, ev.data); err != nil {
				t.Fatalf("%s: %v", ev.typ, err)
			}
		}
	}
	items := e.store.Items()
	if len(items) != len(events) {
		t.Fatalf("items %d, want %d", len(items), len(events))
	}
	for _, ev := range events {
		var found bool
		for _, it := range items {
			if it.Kind == ev.kind && it.ObjectID == ev.object {
				found = true
				if it.SourceReason != ev.reason || it.Status != model.StatusOpen || it.ReportCount != 1 {
					t.Fatalf("%s item %+v", ev.kind, it)
				}
			}
		}
		if !found {
			t.Fatalf("no %s item", ev.kind)
		}
	}
	if len(e.store.Events()) != 0 || len(e.store.Audit()) != 0 {
		t.Fatal("opening items published or audited something")
	}
}

func TestSubmitDeduplicatesAndCountsReports(t *testing.T) {
	e := newEnv(t)
	listing, msg := uuid.New(), uuid.New()
	first := e.submit(model.KindListing, listing, "")
	// A second submission of the same object (a new event) adds no item.
	if again := e.submit(model.KindListing, listing, ""); again != first {
		t.Fatal("second open item for the same object")
	}
	// Reports on one message bump one counter.
	report := e.submit(model.KindReport, msg, "spam")
	for range 2 {
		e.submit(model.KindReport, msg, "harassment")
	}
	if it, _ := e.store.Item(report); it.ReportCount != 3 || it.SourceReason != "spam" {
		t.Fatalf("report item %+v", it)
	}
	// The same message may also be held for review: another kind, another item.
	if held := e.submit(model.KindChatMessage, msg, "newcomer"); held == report {
		t.Fatal("held message merged into the report item")
	}
	// Once decided, a new report opens a new item.
	if err := e.svc.Decide(e.ctx, mod("moderator"), report, model.Decision{Approve: true}); err != nil {
		t.Fatal(err)
	}
	if next := e.submit(model.KindReport, msg, "spam"); next == report {
		t.Fatal("report counted on a decided item")
	}

	// Prose in a reason code is dropped, not stored.
	prose := uuid.New()
	id := e.submit(model.KindChatMessage, prose, "Call me at +7 701 000 0000")
	if it, _ := e.store.Item(id); it.SourceReason != "" {
		t.Fatalf("free text stored as reason: %q", it.SourceReason)
	}

	invalid := []model.Submission{
		{Kind: "poll", ObjectID: uuid.New()},
		{Kind: model.KindListing, ObjectID: uuid.Nil},
	}
	for _, sub := range invalid {
		if err := e.svc.Submit(e.ctx, uuid.New(), sub); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("%+v: %v", sub, err)
		}
	}
	if err := e.svc.Submit(e.ctx, uuid.Nil, model.Submission{Kind: model.KindListing, ObjectID: uuid.New()}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil event: %v", err)
	}

	// A failed transaction leaves the event unprocessed, so the redelivery opens the item.
	event, obj := uuid.New(), uuid.New()
	for _, method := range []string{"InsertItem", "MarkProcessed"} {
		e.store.FailOn(method, errors.New("database down"))
		if err := e.svc.Submit(e.ctx, event, model.Submission{Kind: model.KindReview, ObjectID: obj}); err == nil {
			t.Fatalf("%s failure not returned", method)
		}
		e.store.FailOn(method, nil)
	}
	e.store.FailOn("UpsertReportItem", errors.New("database down"))
	if err := e.svc.Submit(e.ctx, event, model.Submission{Kind: model.KindReport, ObjectID: obj}); err == nil {
		t.Fatal("report upsert failure not returned")
	}
	e.store.FailOn("UpsertReportItem", nil)
	if err := e.svc.Submit(e.ctx, event, model.Submission{Kind: model.KindReview, ObjectID: obj}); err != nil {
		t.Fatal(err)
	}
	var n int
	for _, it := range e.store.Items() {
		if it.ObjectID == obj {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("items after redelivery: %d", n)
	}
}

func TestListQueueFiltersAndPages(t *testing.T) {
	e := newEnv(t)
	moderator := mod("moderator")
	var listings []uuid.UUID
	for i := range model.PageSize + 5 {
		obj := uuid.New()
		e.sources[model.KindListing].Put(obj, model.Summary{Text: "fake listing", PhotoURL: "https://example.invalid/p.jpg"})
		listings = append(listings, e.submit(model.KindListing, obj, ""))
		e.clock.Advance(time.Second)
		if i == 3 {
			review := uuid.New()
			e.sources[model.KindReview].Put(review, model.Summary{Text: "fake review", PhotoURL: "https://example.invalid/r.jpg"})
			e.submit(model.KindReview, review, "")
			e.clock.Advance(time.Second)
		}
	}

	page, err := e.svc.ListQueue(e.ctx, moderator, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != model.PageSize || page.NextCursor == "" {
		t.Fatalf("first page: %d items, cursor %q", len(page.Items), page.NextCursor)
	}
	if page.Items[0].Item.ID != listings[0] || page.Items[4].Item.Kind != model.KindReview {
		t.Fatal("not oldest first")
	}
	if page.Items[4].Summary.PhotoURL != "" {
		t.Fatal("photo shown for a review (listings only)")
	}
	if page.Items[0].Summary.PhotoURL == "" || page.Items[0].Summary.Text != "fake listing" {
		t.Fatalf("listing summary %+v", page.Items[0].Summary)
	}
	next, err := e.svc.ListQueue(e.ctx, moderator, "", page.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 6 || next.NextCursor != "" || next.Items[5].Item.ID != listings[len(listings)-1] {
		t.Fatalf("second page: %d items, cursor %q", len(next.Items), next.NextCursor)
	}
	seen := map[uuid.UUID]bool{}
	for _, it := range append(page.Items, next.Items...) {
		if seen[it.Item.ID] {
			t.Fatal("item on two pages")
		}
		seen[it.Item.ID] = true
	}

	reviews, err := e.svc.ListQueue(e.ctx, mod("admin"), model.KindReview, "")
	if err != nil || len(reviews.Items) != 1 || reviews.Items[0].Item.Kind != model.KindReview {
		t.Fatalf("kind filter: %+v %v", reviews, err)
	}
	// Decided items leave the queue.
	if err := e.svc.Decide(e.ctx, moderator, reviews.Items[0].Item.ID, model.Decision{Approve: true}); err != nil {
		t.Fatal(err)
	}
	if reviews, _ := e.svc.ListQueue(e.ctx, moderator, model.KindReview, ""); len(reviews.Items) != 0 {
		t.Fatal("decided item still queued")
	}

	for _, c := range []struct {
		kind   model.Kind
		cursor string
	}{{"poll", ""}, {"", "!!"}, {"", strings.Repeat("A", model.MaxCursorLen+1)}} {
		if _, err := e.svc.ListQueue(e.ctx, moderator, c.kind, c.cursor); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("kind %q cursor %q: %v", c.kind, c.cursor, err)
		}
	}
	e.store.FailOn("ListOpenItems", errors.New("database down"))
	if _, err := e.svc.ListQueue(e.ctx, moderator, "", ""); err == nil {
		t.Fatal("store failure not returned")
	}
}

func TestListQueueSummaries(t *testing.T) {
	e := newEnv(t)
	moderator := mod("moderator")
	long := strings.Repeat("ж", model.MaxSummaryLen+10)
	trimmed, gone, bigPhoto := uuid.New(), uuid.New(), uuid.New()
	e.sources[model.KindChatMessage].Put(trimmed, model.Summary{Text: long, PhotoURL: "https://example.invalid/x.jpg"})
	e.sources[model.KindListing].Put(bigPhoto, model.Summary{Text: fakeContent, PhotoURL: "https://example.invalid/" + strings.Repeat("a", 2100)})
	e.submit(model.KindChatMessage, trimmed, "link")
	e.clock.Advance(time.Second)
	e.submit(model.KindListing, gone, "") // the source no longer has it
	e.clock.Advance(time.Second)
	e.submit(model.KindListing, bigPhoto, "")

	page, err := e.svc.ListQueue(e.ctx, moderator, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := page.Items[0].Summary; len([]rune(got.Text)) != model.MaxSummaryLen || got.PhotoURL != "" {
		t.Fatalf("chat summary: %d runes, photo %q", len([]rune(got.Text)), got.PhotoURL)
	}
	if got := page.Items[1].Summary; got != (model.Summary{}) {
		t.Fatalf("missing object summary %+v", got)
	}
	if page.Items[2].Summary.PhotoURL != "" {
		t.Fatal("photo URL above the contract limit shown")
	}

	// An outage fails the page: no item without its content.
	e.sources[model.KindListing].Fail = errors.New("sharing down")
	if _, err := e.svc.ListQueue(e.ctx, moderator, "", ""); err == nil {
		t.Fatal("page shown while a source is down")
	}
	e.sources[model.KindListing].Fail = nil
	delete(e.svc.Sources, model.KindListing)
	if _, err := e.svc.ListQueue(e.ctx, moderator, "", ""); err == nil {
		t.Fatal("page shown without a content source")
	}
	// Other kinds stay listable.
	if p, err := e.svc.ListQueue(e.ctx, moderator, model.KindChatMessage, ""); err != nil || len(p.Items) != 1 {
		t.Fatalf("chat only: %v", err)
	}
}

func TestDecideAppliesEveryDecisionKind(t *testing.T) {
	type tc struct {
		name       string
		kind       model.Kind
		d          model.Decision
		wantAudits []string
	}
	var cases []tc
	for _, k := range model.Kinds {
		cases = append(cases,
			tc{"approve " + string(k), k, model.Decision{Approve: true}, []string{model.AuditActionApprove}},
			tc{"reject " + string(k), k, model.Decision{Reason: "unsafe"}, []string{model.AuditActionReject}},
		)
	}
	cases = append(cases,
		tc{"block author on a report", model.KindReport, model.Decision{Reason: "spam", BlockAuthor: true},
			[]string{model.AuditActionReject, model.AuditActionBlock}},
		tc{"legal hold on a chat message", model.KindChatMessage, model.Decision{Reason: "unsafe", LegalHold: true},
			[]string{model.AuditActionReject, model.AuditActionLegalHold}},
		tc{"both", model.KindReport, model.Decision{Reason: "other", BlockAuthor: true, LegalHold: true},
			[]string{model.AuditActionReject, model.AuditActionBlock, model.AuditActionLegalHold}},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			obj := uuid.New()
			id := e.submit(c.kind, obj, "")
			p := mod("admin")
			if err := e.svc.Decide(e.ctx, p, id, c.d); err != nil {
				t.Fatal(err)
			}
			it, _ := e.store.Item(id)
			if it.Status != model.StatusDecided || it.DecidedBy != p.IdentityID || !it.DecidedAt.Equal(e.clock.Now()) || it.Decision != c.d {
				t.Fatalf("item %+v", it)
			}
			audits := e.store.Audit()
			if len(audits) != len(c.wantAudits) {
				t.Fatalf("audits %+v", audits)
			}
			for i, a := range audits {
				if a.Action != c.wantAudits[i] || a.ActorID != p.IdentityID || a.ActorRole != "admin" ||
					a.ObjectType != model.AuditObjectItem || a.ObjectID != id {
					t.Fatalf("audit %d: %+v", i, a)
				}
				if (a.Action == model.AuditActionReject) != (a.Reason != "") {
					t.Fatalf("audit reason on %s: %q", a.Action, a.Reason)
				}
			}
			evs := e.store.Events()
			if len(evs) != 1 || evs[0].Type != model.EventDecided {
				t.Fatalf("events %+v", evs)
			}
			want := model.DecidedData{ItemID: id, Kind: c.kind, ObjectID: obj, Approve: c.d.Approve, Reason: c.d.Reason, BlockAuthor: c.d.BlockAuthor, LegalHold: c.d.LegalHold}
			if evs[0].Data.(model.DecidedData) != want {
				t.Fatalf("event %+v, want %+v", evs[0].Data, want)
			}
		})
	}
}

func TestDecideValidationAndIdempotency(t *testing.T) {
	e := newEnv(t)
	listing := e.submit(model.KindListing, uuid.New(), "")
	report := e.submit(model.KindReport, uuid.New(), "spam")
	moderator := mod("moderator")

	invalid := []struct {
		name string
		item uuid.UUID
		d    model.Decision
	}{
		{"reject without reason", listing, model.Decision{}},
		{"unknown reason", listing, model.Decision{Reason: "boring"}},
		{"reason on approval", listing, model.Decision{Approve: true, Reason: "spam"}},
		{"block author of a listing", listing, model.Decision{Reason: "spam", BlockAuthor: true}},
		{"legal hold on a listing", listing, model.Decision{Reason: "spam", LegalHold: true}},
		{"block author when approving", report, model.Decision{Approve: true, BlockAuthor: true}},
		{"legal hold when approving", report, model.Decision{Approve: true, LegalHold: true}},
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			if err := e.svc.Decide(e.ctx, moderator, c.item, c.d); !errors.Is(err, model.ErrInvalidInput) {
				t.Fatalf("want invalid input, got %v", err)
			}
		})
	}
	if len(e.store.Audit())+len(e.store.Events()) != 0 {
		t.Fatal("an invalid decision left a trace")
	}
	for _, id := range []uuid.UUID{uuid.Nil, uuid.New()} {
		if err := e.svc.Decide(e.ctx, moderator, id, model.Decision{Approve: true}); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("item %v: %v", id, err)
		}
	}

	// A decision is final: the second one conflicts and writes nothing.
	if err := e.svc.Decide(e.ctx, moderator, listing, model.Decision{Approve: true}); err != nil {
		t.Fatal(err)
	}
	for _, d := range []model.Decision{{Approve: true}, {Reason: "spam"}} {
		if err := e.svc.Decide(e.ctx, mod("admin"), listing, d); !errors.Is(err, model.ErrAlreadyDecided) {
			t.Fatalf("second decision: %v", err)
		}
	}
	if len(e.store.Audit()) != 1 || len(e.store.Events()) != 1 {
		t.Fatalf("second decision wrote: %d audits, %d events", len(e.store.Audit()), len(e.store.Events()))
	}
	if it, _ := e.store.Item(listing); !it.Decision.Approve {
		t.Fatal("first decision overwritten")
	}
}

// lostRaceStore makes DecideItem find the item no longer open, as when a
// concurrent decision committed between the read and the update.
type lostRaceStore struct{ *moderationtest.MemStore }

type lostRaceTx struct{ port.Tx }

func (s lostRaceStore) InTx(ctx context.Context, fn func(port.Tx) error) error {
	return s.MemStore.InTx(ctx, func(tx port.Tx) error { return fn(lostRaceTx{tx}) })
}

func (lostRaceTx) DecideItem(context.Context, uuid.UUID, uuid.UUID, time.Time, model.Decision) (bool, error) {
	return false, nil
}

func TestDecideFailuresRollBack(t *testing.T) {
	e := newEnv(t)
	moderator := mod("moderator")
	id := e.submit(model.KindReport, uuid.New(), "spam")
	d := model.Decision{Reason: "spam", BlockAuthor: true, LegalHold: true}
	for _, method := range []string{"GetItemForUpdate", "DecideItem", "Audit", "Publish", "InTx"} {
		e.store.FailOn(method, errors.New("database down"))
		if err := e.svc.Decide(e.ctx, moderator, id, d); err == nil || errors.Is(err, model.ErrAlreadyDecided) {
			t.Fatalf("%s failure: %v", method, err)
		}
		e.store.FailOn(method, nil)
		if it, _ := e.store.Item(id); it.Status != model.StatusOpen {
			t.Fatalf("%s failure left the item decided", method)
		}
		if len(e.store.Audit())+len(e.store.Events()) != 0 {
			t.Fatalf("%s failure left audit or events", method)
		}
	}

	e.svc.Store = lostRaceStore{e.store}
	if err := e.svc.Decide(e.ctx, moderator, id, d); !errors.Is(err, model.ErrAlreadyDecided) {
		t.Fatalf("lost race: %v", err)
	}
	if len(e.store.Audit())+len(e.store.Events()) != 0 {
		t.Fatal("lost race wrote audit or events")
	}
}

func TestStaffRolesPerOperation(t *testing.T) {
	e := newEnv(t)
	id := e.submit(model.KindVerification, uuid.New(), "")
	denied := map[string]struct {
		p    auth.Principal
		want error
	}{
		"admin without MFA":        {auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}}, model.ErrForbidden},
		"support":                  {mod("support"), model.ErrForbidden},
		"no roles":                 {mod(), model.ErrForbidden},
		"member with a staff role": {auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New(), Roles: []string{"admin"}, MFA: true}, model.ErrUnauthorized},
	}
	for name, c := range denied {
		t.Run(name, func(t *testing.T) {
			if _, err := e.svc.ListQueue(e.ctx, c.p, "", ""); !errors.Is(err, c.want) {
				t.Fatalf("queue: %v", err)
			}
			if err := e.svc.Decide(e.ctx, c.p, id, model.Decision{Approve: true}); !errors.Is(err, c.want) {
				t.Fatalf("decide: %v", err)
			}
		})
	}
	// A multi-role principal acts as admin when it has that role.
	both := mod("moderator", "admin")
	if err := e.svc.Decide(e.ctx, both, id, model.Decision{Approve: true}); err != nil {
		t.Fatal(err)
	}
	if a := e.store.Audit(); len(a) != 1 || a[0].ActorRole != "admin" {
		t.Fatalf("audit %+v", a)
	}
}
