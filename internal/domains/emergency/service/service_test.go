package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/emergencytest"
	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/crypto"
)

// Clearly fake personal data: never real numbers, names or texts.
const (
	fakeText    = "example sos text need medicine"
	fakeCard    = "example child card calm with music"
	fakeName    = "Example Contact"
	fakePhone   = "+70000000001"
	fakeDisplay = "Examplename"
)

var secrets = []string{fakeText, fakeCard, fakeName, fakePhone, fakeDisplay}

type env struct {
	svc     *service.Service
	store   *emergencytest.MemoryStore
	members *emergencytest.FakeMembers
	clock   *emergencytest.Clock
	logs    *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	logs := &bytes.Buffer{}
	e := &env{
		store:   emergencytest.NewMemoryStore(),
		members: emergencytest.NewFakeMembers(),
		clock:   &emergencytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		logs:    logs,
	}
	e.svc = &service.Service{
		Store:         e.store,
		Sealer:        &emergencytest.FakeSealer{},
		Members:       e.members,
		Blocks:        e.members,
		Acquaintances: e.members,
		Clock:         e.clock,
		Logger:        slog.New(slog.NewTextHandler(&lockedWriter{w: logs}, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	t.Cleanup(func() {
		// No personal data may ever reach the logs or the events.
		out := logs.String()
		payload, err := json.Marshal(e.store.Events())
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if strings.Contains(out, s) {
				t.Errorf("logs contain personal data %q", s)
			}
			if bytes.Contains(payload, []byte(s)) {
				t.Errorf("events contain personal data %q", s)
			}
		}
	})
	return e
}

type lockedWriter struct {
	mu sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// member returns a member session and registers her profile.
func (e *env) member(verified bool, district *uuid.UUID) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{Verified: verified, DistrictID: district, DisplayName: fakeDisplay, Lang: "kk"})
	return p
}

func (e *env) raise(t *testing.T, p auth.Principal, typ string) model.SosView {
	t.Helper()
	v, err := e.svc.RaiseSos(context.Background(), p, uuid.Must(uuid.NewV7()), typ, fakeText)
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	return v
}

func (e *env) addExternal(t *testing.T, owner auth.Principal) model.ContactView {
	t.Helper()
	c, err := e.svc.AddTrustedContact(context.Background(), owner, uuid.Must(uuid.NewV7()),
		model.NewTrustedContact{Kind: model.ContactExternal, Name: fakeName, Phone: fakePhone})
	if err != nil {
		t.Fatalf("add external: %v", err)
	}
	return c
}

// addMember invites friend (after they met) and lets her accept.
func (e *env) addMember(t *testing.T, owner, friend auth.Principal, accept bool) model.ContactView {
	t.Helper()
	ctx := context.Background()
	e.members.Meet(owner.MemberID, friend.MemberID)
	c, err := e.svc.AddTrustedContact(ctx, owner, uuid.Must(uuid.NewV7()), model.NewTrustedContact{Kind: model.ContactMember, MemberID: friend.MemberID})
	if err != nil {
		t.Fatalf("invite: %v", err)
	}
	if accept {
		if err := e.svc.AcceptTrustedInvitation(ctx, friend, c.ID); err != nil {
			t.Fatalf("accept: %v", err)
		}
	}
	return c
}

func district() *uuid.UUID {
	d := uuid.New()
	return &d
}

func TestRaiseSosIsAtomicAndAlertsConfirmedContactsOnly(t *testing.T) {
	e := newEnv(t)
	owner := e.member(false, nil)
	friend := e.member(true, nil)
	invited := e.member(true, nil)
	ext := e.addExternal(t, owner)
	e.addMember(t, owner, friend, true)
	e.addMember(t, owner, invited, false)
	before := len(e.store.Events())

	v := e.raise(t, owner, model.SosChildcare)
	if v.PlainText != fakeText || v.Status != model.SosOpen || v.Shared() || !v.ClosesAt.Equal(e.clock.Now().Add(24*time.Hour)) {
		t.Fatalf("view = %+v", v)
	}
	stored, _ := e.store.Sos(v.ID)
	if !bytes.HasPrefix(stored.Text, []byte("sealed|"+owner.MemberID.String()+"|"+string(crypto.AssociatedData(model.FieldSosText, v.ID))+"|")) {
		t.Fatal("text is not sealed with the author's key and emergency.sos.text|<id>")
	}
	events := e.store.Events()[before:]
	if len(events) != 4 { // SosRaised, moderators, member contact, external contact
		t.Fatalf("events = %d: %+v", len(events), events)
	}
	raised := e.store.EventsOfType(model.EventSosRaised)
	if len(raised) != 1 || raised[0].Data.(model.SosRaisedData) != (model.SosRaisedData{SosID: v.ID, MemberID: owner.MemberID, Type: model.SosChildcare}) {
		t.Fatalf("raised = %+v", raised)
	}
	var staff, toFriend, toExternal int
	for _, d := range e.store.Deliveries() {
		if d.Params["sos_id"] != v.ID.String() {
			continue
		}
		if d.Class != model.ClassCritical || d.RequestID == uuid.Nil || len(d.Params) != 1 {
			t.Fatalf("delivery = %+v", d)
		}
		switch {
		case d.Recipient.StaffRole == model.StaffModerator && d.Template == model.TemplateSosRaised:
			staff++
		case d.Recipient.MemberID != nil && *d.Recipient.MemberID == friend.MemberID && d.Template == model.TemplateSosRaisedContact:
			toFriend++
		case d.Recipient.TrustedContactID != nil && *d.Recipient.TrustedContactID == ext.ID && d.Template == model.TemplateSosRaisedContact:
			toExternal++
		case d.Recipient.MemberID != nil && *d.Recipient.MemberID == invited.MemberID:
			t.Fatal("an invited (unconfirmed) contact was alerted")
		default:
			t.Fatalf("unexpected delivery %+v", d)
		}
	}
	if staff != 1 || toFriend != 1 || toExternal != 1 {
		t.Fatalf("staff=%d friend=%d external=%d", staff, toFriend, toExternal)
	}
	if tm, ok := e.store.Timer(v.ID); !ok || tm.Cancelled || !tm.DueAt.Equal(v.ClosesAt) {
		t.Fatalf("timer = %+v %v", tm, ok)
	}
}

func TestRaiseSosRollsBackOnFailure(t *testing.T) {
	for _, method := range []string{"ScheduleAutoClose", "Publish:" + model.EventDeliveryRequested, "Publish:" + model.EventSosRaised, "InsertSos"} {
		t.Run(method, func(t *testing.T) {
			e := newEnv(t)
			p := e.member(false, nil)
			errInjected := errors.New("injected")
			e.store.FailOn = func(m string) error {
				if m == method {
					return errInjected
				}
				return nil
			}
			if _, err := e.svc.RaiseSos(context.Background(), p, uuid.New(), model.SosOther, fakeText); !errors.Is(err, errInjected) {
				t.Fatalf("err = %v", err)
			}
			if e.store.SosCount() != 0 || len(e.store.Events()) != 0 {
				t.Fatalf("partial commit: sos=%d events=%d", e.store.SosCount(), len(e.store.Events()))
			}
		})
	}
}

func TestRaiseSosIdempotencyAndRateLimit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.member(false, nil)
	first := uuid.New()
	a, err := e.svc.RaiseSos(ctx, p, first, model.SosOther, fakeText)
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.RaiseSos(ctx, p, first, model.SosItemOrMedicine, "another text")
	if err != nil || again.ID != a.ID || again.Type != model.SosOther || again.PlainText != fakeText {
		t.Fatalf("replay = %+v %v", again, err)
	}
	if n := len(e.store.EventsOfType(model.EventSosRaised)); n != 1 {
		t.Fatalf("raised events after replay = %d", n)
	}
	e.clock.Advance(time.Minute)
	e.raise(t, p, model.SosOther)
	e.raise(t, p, model.SosChildcare)
	if _, err := e.svc.RaiseSos(ctx, p, uuid.New(), model.SosOther, fakeText); !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("4th within an hour: %v", err)
	}
	// A replay is not a new SOS and still answers while limited.
	if v, err := e.svc.RaiseSos(ctx, p, first, model.SosOther, fakeText); err != nil || v.ID != a.ID {
		t.Fatalf("replay while limited: %v", err)
	}
	// Another member is not limited by her.
	e.raise(t, e.member(false, nil), model.SosOther)
	// One hour after the first (but not the other two), one slot frees up.
	e.clock.Advance(59*time.Minute + 30*time.Second)
	e.raise(t, p, model.SosOther)
	if _, err := e.svc.RaiseSos(ctx, p, uuid.New(), model.SosOther, fakeText); !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("limit after the window moved: %v", err)
	}
}

func TestRaiseSosValidationAndAuth(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.member(false, nil)
	cases := []struct {
		name string
		p    auth.Principal
		key  uuid.UUID
		typ  string
		text string
		want error
	}{
		{"staff", auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"moderator"}}, uuid.New(), model.SosOther, fakeText, model.ErrUnauthorized},
		{"anonymous", auth.Principal{}, uuid.New(), model.SosOther, fakeText, model.ErrUnauthorized},
		{"no member id", auth.Principal{IdentityID: uuid.New()}, uuid.New(), model.SosOther, fakeText, model.ErrUnauthorized},
		{"no key", p, uuid.Nil, model.SosOther, fakeText, model.ErrInvalidInput},
		{"bad type", p, uuid.New(), "feel_bad", fakeText, model.ErrInvalidInput},
		{"blank", p, uuid.New(), model.SosOther, "   ", model.ErrInvalidInput},
		{"too long", p, uuid.New(), model.SosOther, strings.Repeat("я", 301), model.ErrInvalidInput},
	}
	for _, c := range cases {
		if _, err := e.svc.RaiseSos(ctx, c.p, c.key, c.typ, c.text); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if _, err := e.svc.RaiseSos(ctx, p, uuid.New(), model.SosOther, strings.Repeat("я", 300)); err != nil {
		t.Fatalf("300 characters: %v", err)
	}
}

func TestCloseAndAutoClose(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.member(false, nil)
	v := e.raise(t, p, model.SosOther)
	if _, err := e.svc.CloseSos(ctx, e.member(false, nil), v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("close by another member: %v", err)
	}
	closed, err := e.svc.CloseSos(ctx, p, v.ID)
	if err != nil || closed.Status != model.SosClosed || closed.PlainText != fakeText {
		t.Fatalf("close: %+v %v", closed, err)
	}
	if _, err := e.svc.CloseSos(ctx, p, v.ID); err != nil {
		t.Fatalf("close twice: %v", err)
	}
	if n := len(e.store.EventsOfType(model.EventSosClosed)); n != 1 {
		t.Fatalf("closed events = %d", n)
	}
	if tm, _ := e.store.Timer(v.ID); !tm.Cancelled {
		t.Fatal("auto-close timer not cancelled")
	}
	// The auto-close handler closes an open SOS once.
	w := e.raise(t, p, model.SosItemOrMedicine)
	for range 2 {
		tx, commit := e.store.Tx()
		if err := e.svc.HandleAutoClose(ctx, tx, w.ID); err != nil {
			t.Fatal(err)
		}
		commit()
	}
	got, _ := e.store.Sos(w.ID)
	if got.Status != model.SosClosed || got.CloseReason == nil || *got.CloseReason != model.CloseAuto {
		t.Fatalf("auto-closed = %+v", got)
	}
	if n := len(e.store.EventsOfType(model.EventSosClosed)); n != 2 {
		t.Fatalf("closed events = %d, want 2", n)
	}
	// Unknown subject: nothing happens.
	tx, commit := e.store.Tx()
	if err := e.svc.HandleAutoClose(ctx, tx, uuid.New()); err != nil {
		t.Fatal(err)
	}
	commit()
}

func TestShareRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := district()
	verified := e.member(true, d)
	unverified := e.member(false, d)

	// Childcare is never shareable (N5), not even for a verified member.
	child := e.raise(t, verified, model.SosChildcare)
	if _, err := e.svc.ShareSos(ctx, verified, child.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("share childcare: %v", err)
	}
	if s, _ := e.store.Sos(child.ID); s.Shared() {
		t.Fatal("childcare SOS was shared")
	}
	// Unverified members cannot share.
	u := e.raise(t, unverified, model.SosOther)
	if _, err := e.svc.ShareSos(ctx, unverified, u.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("share unverified: %v", err)
	}
	// Someone else's SOS is not found.
	v := e.raise(t, verified, model.SosItemOrMedicine)
	if _, err := e.svc.ShareSos(ctx, e.member(true, d), v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("share another's: %v", err)
	}
	shared, err := e.svc.ShareSos(ctx, verified, v.ID)
	if err != nil || !shared.Shared() || *shared.DistrictID != *d {
		t.Fatalf("share: %+v %v", shared, err)
	}
	if _, err := e.svc.ShareSos(ctx, verified, v.ID); err != nil {
		t.Fatalf("share twice: %v", err)
	}
	ev := e.store.EventsOfType(model.EventSosShared)
	if len(ev) != 1 || ev[0].Data.(model.SosSharedData) != (model.SosSharedData{SosID: v.ID, DistrictID: *d}) {
		t.Fatalf("shared events = %+v", ev)
	}
	// A closed SOS cannot be shared.
	c := e.raise(t, verified, model.SosOther)
	if _, err := e.svc.CloseSos(ctx, verified, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ShareSos(ctx, verified, c.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("share closed: %v", err)
	}
	// No district: conflict.
	nod := e.member(true, nil)
	n := e.raise(t, nod, model.SosOther)
	if _, err := e.svc.ShareSos(ctx, nod, n.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("share without district: %v", err)
	}
}

func TestFeedVisibility(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := district()
	viewer := e.member(true, d)
	author := e.member(true, d)
	blocker := e.member(true, d)
	blockedByViewer := e.member(true, d)
	elsewhere := e.member(true, district())

	share := func(p auth.Principal, typ string) model.SosView {
		v, err := e.svc.RaiseSos(ctx, p, uuid.Must(uuid.NewV7()), typ, fakeText)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.ShareSos(ctx, p, v.ID); err != nil {
			t.Fatal(err)
		}
		return v
	}
	visible := share(author, model.SosItemOrMedicine)
	own := share(viewer, model.SosOther) // never in her own feed
	share(blocker, model.SosOther)
	share(blockedByViewer, model.SosOther)
	share(elsewhere, model.SosOther)
	e.raise(t, author, model.SosOther) // not shared
	e.members.Block(blocker.MemberID, viewer.MemberID)
	e.members.Block(viewer.MemberID, blockedByViewer.MemberID)
	// A childcare row that somehow got shared (bypassing the service and the
	// table check) must still never be shown.
	childID := uuid.Must(uuid.NewV7())
	sealed, _ := (&emergencytest.FakeSealer{}).Seal(ctx, author.MemberID, []byte(fakeText), crypto.AssociatedData(model.FieldSosText, childID))
	now := e.clock.Now()
	e.store.PutSos(model.Sos{
		ID: childID, MemberID: author.MemberID, IdempotencyKey: uuid.New(), Type: model.SosChildcare, Text: sealed,
		Status: model.SosOpen, CreatedAt: now, ClosesAt: now.Add(time.Hour), DistrictID: d, SharedAt: &now,
	})

	page, err := e.svc.Feed(ctx, viewer, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != visible.ID || page.Items[0].Text != fakeText ||
		page.Items[0].DisplayName != fakeDisplay || page.Items[0].RespondedByMe {
		t.Fatalf("feed = %+v", page.Items)
	}
	for _, it := range page.Items {
		if it.Type == model.SosChildcare {
			t.Fatal("childcare in the feed")
		}
	}
	// The blocked members do not see the viewer's SOS either.
	for _, other := range []auth.Principal{blocker, blockedByViewer} {
		pg, err := e.svc.Feed(ctx, other, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range pg.Items {
			if it.ID == own.ID {
				t.Fatal("a blocked member sees the viewer's SOS")
			}
		}
		if len(pg.Items) != 2 { // author's and the other blocked member's, not the viewer's
			t.Fatalf("blocked member's feed has %d items", len(pg.Items))
		}
	}
	// Verified only.
	if _, err := e.svc.Feed(ctx, e.member(false, d), ""); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unverified feed: %v", err)
	}
	if _, err := e.svc.Feed(ctx, viewer, "%%%"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad cursor: %v", err)
	}
	// Expired (past closes_at, timer late) is not shown.
	e.clock.Advance(25 * time.Hour)
	if pg, err := e.svc.Feed(ctx, viewer, ""); err != nil || len(pg.Items) != 0 {
		t.Fatalf("expired feed: %+v %v", pg, err)
	}
}

func TestFeedPagination(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := district()
	viewer := e.member(true, d)
	for range model.FeedPageLimit + 5 {
		a := e.member(true, d)
		v := e.raise(t, a, model.SosOther)
		if _, err := e.svc.ShareSos(ctx, a, v.ID); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(time.Second)
	}
	first, err := e.svc.Feed(ctx, viewer, "")
	if err != nil || len(first.Items) != model.FeedPageLimit || first.NextCursor == "" {
		t.Fatalf("first page: %d %q %v", len(first.Items), first.NextCursor, err)
	}
	second, err := e.svc.Feed(ctx, viewer, first.NextCursor)
	if err != nil || len(second.Items) != 5 || second.NextCursor != "" {
		t.Fatalf("second page: %d %q %v", len(second.Items), second.NextCursor, err)
	}
	if !second.Items[0].CreatedAt.Before(first.Items[len(first.Items)-1].CreatedAt) {
		t.Fatal("pages overlap or are out of order")
	}
}

func TestRespondAndAccept(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := district()
	author := e.member(true, d)
	helper := e.member(true, d)
	stranger := e.member(true, d)
	v := e.raise(t, author, model.SosItemOrMedicine)

	// Not shared yet: not in the feed, so not found.
	if err := e.svc.RespondToSos(ctx, helper, v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("respond to unshared: %v", err)
	}
	if _, err := e.svc.ShareSos(ctx, author, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RespondToSos(ctx, e.member(false, d), v.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unverified respond: %v", err)
	}
	if err := e.svc.RespondToSos(ctx, e.member(true, district()), v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("other district respond: %v", err)
	}
	if err := e.svc.RespondToSos(ctx, author, v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("respond to own: %v", err)
	}
	for range 2 {
		if err := e.svc.RespondToSos(ctx, helper, v.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.RespondToSos(ctx, stranger, v.ID); err != nil {
		t.Fatal(err)
	}
	var toAuthor []model.DeliveryRequestedData
	for _, dl := range e.store.Deliveries() {
		if dl.Template == model.TemplateSosResponded {
			toAuthor = append(toAuthor, dl)
		}
	}
	if len(toAuthor) != 2 || toAuthor[0].RequestID == toAuthor[1].RequestID || toAuthor[0].Class != model.ClassNormal ||
		*toAuthor[0].Recipient.MemberID != author.MemberID {
		t.Fatalf("author notifications = %+v", toAuthor)
	}
	if pg, _ := e.svc.Feed(ctx, helper, ""); len(pg.Items) != 1 || !pg.Items[0].RespondedByMe {
		t.Fatalf("respondedByMe: %+v", pg.Items)
	}

	// Responses: author only, metInPerson from Acquaintances, blocked hidden.
	e.members.Meet(author.MemberID, helper.MemberID)
	if _, err := e.svc.ListResponses(ctx, helper, v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("list by non-author: %v", err)
	}
	list, err := e.svc.ListResponses(ctx, author, v.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %+v %v", list, err)
	}
	for _, r := range list {
		if (r.ResponderID == helper.MemberID) != r.MetInPerson || r.DisplayName != fakeDisplay {
			t.Fatalf("response %+v", r)
		}
	}
	var helperResp, strangerResp uuid.UUID
	for _, r := range list {
		if r.ResponderID == helper.MemberID {
			helperResp = r.ID
		} else {
			strangerResp = r.ID
		}
	}
	e.members.Block(author.MemberID, stranger.MemberID)
	if list, _ := e.svc.ListResponses(ctx, author, v.ID); len(list) != 1 || list[0].ResponderID != helper.MemberID {
		t.Fatalf("blocked responder shown: %+v", list)
	}
	if err := e.svc.AcceptResponse(ctx, author, v.ID, strangerResp); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("accept blocked: %v", err)
	}

	// Accept: author only, idempotent, one event.
	if err := e.svc.AcceptResponse(ctx, helper, v.ID, helperResp); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("accept by non-author: %v", err)
	}
	other := e.raise(t, author, model.SosOther)
	if err := e.svc.AcceptResponse(ctx, author, other.ID, helperResp); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("accept response of another sos: %v", err)
	}
	for range 2 {
		if err := e.svc.AcceptResponse(ctx, author, v.ID, helperResp); err != nil {
			t.Fatal(err)
		}
	}
	acc := e.store.EventsOfType(model.EventSosResponseAccepted)
	if len(acc) != 1 || acc[0].Data.(model.SosResponseAcceptedData) != (model.SosResponseAcceptedData{SosID: v.ID, AuthorID: author.MemberID, ResponderID: helper.MemberID}) {
		t.Fatalf("accepted events = %+v", acc)
	}
	// Closed: no new responses.
	if _, err := e.svc.CloseSos(ctx, author, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RespondToSos(ctx, e.member(true, d), v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("respond to closed: %v", err)
	}
	if err := e.svc.AcceptResponse(ctx, author, v.ID, helperResp); err != nil {
		t.Fatalf("repeat accept after close: %v", err)
	}
}

func TestChildcareNeverRespondable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := district()
	author := e.member(true, d)
	v := e.raise(t, author, model.SosChildcare)
	_, _ = e.svc.ShareSos(ctx, author, v.ID)
	if err := e.svc.RespondToSos(ctx, e.member(true, d), v.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("respond to childcare: %v", err)
	}
}

func TestTrustedContactRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.member(false, nil)
	friend := e.member(true, nil)
	stranger := e.member(true, nil)

	// A member she has not met in person is refused.
	if _, err := e.svc.AddTrustedContact(ctx, owner, uuid.New(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: stranger.MemberID}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unmet member: %v", err)
	}
	// Herself is invalid.
	if _, err := e.svc.AddTrustedContact(ctx, owner, uuid.New(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: owner.MemberID}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("self: %v", err)
	}
	// Met but blocked is refused.
	blocked := e.member(true, nil)
	e.members.Meet(owner.MemberID, blocked.MemberID)
	e.members.Block(blocked.MemberID, owner.MemberID)
	if _, err := e.svc.AddTrustedContact(ctx, owner, uuid.New(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: blocked.MemberID}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("blocked member: %v", err)
	}

	inv := e.addMember(t, owner, friend, false)
	if inv.Status != model.ContactInvited || inv.DisplayName != fakeDisplay || inv.PhoneMasked != "" {
		t.Fatalf("invited = %+v", inv)
	}
	invites := 0
	for _, d := range e.store.Deliveries() {
		if d.Template == model.TemplateTrustedInvitation && *d.Recipient.MemberID == friend.MemberID && d.Params["contact_id"] == inv.ID.String() {
			invites++
		}
	}
	if invites != 1 {
		t.Fatalf("invitations = %d", invites)
	}
	// Inviting the same member again is a conflict.
	if _, err := e.svc.AddTrustedContact(ctx, owner, uuid.New(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: friend.MemberID}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("duplicate member: %v", err)
	}
	// Only the invited member can accept.
	if err := e.svc.AcceptTrustedInvitation(ctx, stranger, inv.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("accept by another member: %v", err)
	}
	if err := e.svc.AcceptTrustedInvitation(ctx, owner, inv.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("accept by the owner: %v", err)
	}
	if c, _ := e.store.Contact(inv.ID); c.Status != model.ContactInvited {
		t.Fatal("confirmed by the wrong member")
	}
	for range 2 {
		if err := e.svc.AcceptTrustedInvitation(ctx, friend, inv.ID); err != nil {
			t.Fatal(err)
		}
	}
	if c, _ := e.store.Contact(inv.ID); c.Status != model.ContactConfirmed {
		t.Fatal("not confirmed")
	}

	// External: masked phone only; idempotent; max 3 active.
	key := uuid.New()
	in := model.NewTrustedContact{Kind: model.ContactExternal, Name: fakeName, Phone: fakePhone}
	ext, err := e.svc.AddTrustedContact(ctx, owner, key, in)
	if err != nil || ext.Status != model.ContactConfirmed || ext.DisplayName != fakeName || ext.PhoneMasked != "+7 *** *** 00 01" {
		t.Fatalf("external = %+v %v", ext, err)
	}
	if again, err := e.svc.AddTrustedContact(ctx, owner, key, in); err != nil || again.ID != ext.ID {
		t.Fatalf("idempotent add: %+v %v", again, err)
	}
	stored, _ := e.store.Contact(ext.ID)
	if !bytes.HasPrefix(stored.Phone, []byte("sealed|"+owner.MemberID.String()+"|"+model.FieldContactPhone+"|"+ext.ID.String()+"|")) {
		t.Fatal("phone not sealed with the owner's key")
	}
	e.addExternal(t, owner)
	if _, err := e.svc.AddTrustedContact(ctx, owner, uuid.New(), in); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("4th contact: %v", err)
	}
	// Invalid external input.
	for _, bad := range []model.NewTrustedContact{
		{Kind: model.ContactExternal, Name: fakeName, Phone: "80000000001"},
		{Kind: model.ContactExternal, Name: " ", Phone: fakePhone},
		{Kind: "friend"},
	} {
		if _, err := e.svc.AddTrustedContact(ctx, e.member(false, nil), uuid.New(), bad); !errors.Is(err, model.ErrInvalidInput) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// Removing frees a slot; removed contacts no longer resolve for notify.
	if err := e.svc.RemoveTrustedContact(ctx, stranger, ext.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("remove another's: %v", err)
	}
	if err := e.svc.RemoveTrustedContact(ctx, owner, ext.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RemoveTrustedContact(ctx, owner, ext.ID); err != nil {
		t.Fatalf("remove twice: %v", err)
	}
	if _, _, err := e.svc.ContactPhone(ctx, ext.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("phone of removed: %v", err)
	}
	if _, _, err := e.svc.TrustedContactTarget(ctx, ext.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("target of removed: %v", err)
	}
	e.addExternal(t, owner)
	plan, err := e.svc.GetPlan(ctx, owner)
	if err != nil || len(plan.Contacts) != 3 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	for _, c := range plan.Contacts {
		if strings.Contains(c.PhoneMasked, "0000000") {
			t.Fatalf("full phone in plan: %+v", c)
		}
	}
}

func TestAcceptRechecksMetInPerson(t *testing.T) {
	e := newEnv(t)
	owner := e.member(false, nil)
	friend := e.member(true, nil)
	inv := e.addMember(t, owner, friend, false)
	// The acquaintance is no longer confirmed (fresh fakes have no pair).
	e.svc.Acquaintances = emergencytest.NewFakeMembers()
	if err := e.svc.AcceptTrustedInvitation(context.Background(), friend, inv.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("accept without met: %v", err)
	}
}

func TestNotifyLookups(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.member(false, nil)
	friend := e.member(true, nil)
	ext := e.addExternal(t, owner)
	phone, lang, err := e.svc.ContactPhone(ctx, ext.ID)
	if err != nil || phone != fakePhone || lang != "kk" {
		t.Fatalf("phone: %q %q %v", phone, lang, err)
	}
	if m, external, err := e.svc.TrustedContactTarget(ctx, ext.ID); err != nil || !external || m != nil {
		t.Fatalf("external target: %v %v %v", m, external, err)
	}
	inv := e.addMember(t, owner, friend, false)
	if _, _, err := e.svc.TrustedContactTarget(ctx, inv.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("invited target: %v", err)
	}
	if err := e.svc.AcceptTrustedInvitation(ctx, friend, inv.ID); err != nil {
		t.Fatal(err)
	}
	if m, external, err := e.svc.TrustedContactTarget(ctx, inv.ID); err != nil || external || m == nil || *m != friend.MemberID {
		t.Fatalf("member target: %v %v %v", m, external, err)
	}
	if _, _, err := e.svc.ContactPhone(ctx, inv.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("phone of member contact: %v", err)
	}
	if _, _, err := e.svc.ContactPhone(ctx, uuid.New()); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("phone of unknown: %v", err)
	}
}

func TestSendPlanAndSharedPlanAccess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.member(false, nil)
	friend := e.member(true, nil)
	invited := e.member(true, nil)
	other := e.member(true, nil)

	if err := e.svc.SendPlan(ctx, owner, uuid.New(), "send"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("lowercase confirm: %v", err)
	}
	if err := e.svc.SendPlan(ctx, owner, uuid.New(), "SEND"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("no card: %v", err)
	}
	if err := e.svc.SetChildCard(ctx, owner, fakeCard); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetChildCard(ctx, owner, strings.Repeat("a", 2001)); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("long card: %v", err)
	}
	if err := e.svc.SendPlan(ctx, owner, uuid.New(), "SEND"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("no confirmed contacts: %v", err)
	}
	fc := e.addMember(t, owner, friend, true)
	e.addMember(t, owner, invited, false)
	ext := e.addExternal(t, owner)

	// Before the send nobody may read the plan.
	if _, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("before send: %v", err)
	}
	key := uuid.New()
	for range 2 {
		if err := e.svc.SendPlan(ctx, owner, key, "SEND"); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.store.EventsOfType(model.EventPlanSent)); n != 1 {
		t.Fatalf("plan sent events = %d", n)
	}
	var toFriend, toExt int
	for _, d := range e.store.Deliveries() {
		if d.Template != model.TemplatePlanSent {
			continue
		}
		if d.Class != model.ClassNormal || d.Params["owner_id"] != owner.MemberID.String() {
			t.Fatalf("plan delivery %+v", d)
		}
		switch {
		case d.Recipient.MemberID != nil && *d.Recipient.MemberID == friend.MemberID:
			toFriend++
		case d.Recipient.TrustedContactID != nil && *d.Recipient.TrustedContactID == ext.ID:
			toExt++
		default:
			t.Fatalf("plan sent to %+v", d.Recipient)
		}
	}
	if toFriend != 1 || toExt != 1 {
		t.Fatalf("friend=%d external=%d", toFriend, toExt)
	}
	plan, err := e.svc.GetPlan(ctx, owner)
	if err != nil || plan.ChildCard != fakeCard || plan.LastSentAt == nil {
		t.Fatalf("plan: %+v %v", plan, err)
	}

	// Access rules.
	for name, p := range map[string]auth.Principal{"invited": invited, "other": other, "owner": owner} {
		if _, err := e.svc.GetSharedPlan(ctx, p, owner.MemberID); !errors.Is(err, model.ErrForbidden) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(e.store.Audit()) != 0 {
		t.Fatal("denied reads were audited as views")
	}
	sp, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID)
	if err != nil || sp.ChildCard != fakeCard || sp.OwnerDisplayName != fakeDisplay || !sp.SentAt.Equal(*plan.LastSentAt) {
		t.Fatalf("shared plan: %+v %v", sp, err)
	}
	audit := e.store.Audit()
	if len(audit) != 1 || audit[0].ActorID != friend.MemberID || audit[0].Action != model.AuditViewSharedPlan ||
		audit[0].ObjectID != owner.MemberID || audit[0].ObjectType != model.AuditObjectChildCard {
		t.Fatalf("audit = %+v", audit)
	}
	// Audit failure: nothing is returned.
	e.store.FailOn = func(m string) error {
		if m == "Audit" {
			return errors.New("audit down")
		}
		return nil
	}
	if _, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID); err == nil {
		t.Fatal("plan returned without an audit entry")
	}
	e.store.FailOn = nil
	// Removed contact loses access.
	if err := e.svc.RemoveTrustedContact(ctx, owner, fc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("removed contact: %v", err)
	}
	// Rate limit on sends (external contacts get an SMS).
	for range model.MaxPlanSendsPerHour - 1 {
		if err := e.svc.SendPlan(ctx, owner, uuid.New(), "SEND"); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.SendPlan(ctx, owner, uuid.New(), "SEND"); !errors.Is(err, model.ErrRateLimited) {
		t.Fatalf("send rate limit: %v", err)
	}
}

func TestBlocksFailClosed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := district()
	author := e.member(true, d)
	viewer := e.member(true, d)
	v := e.raise(t, author, model.SosOther)
	if _, err := e.svc.ShareSos(ctx, author, v.ID); err != nil {
		t.Fatal(err)
	}
	e.svc.Blocks = failingBlocks{}
	if _, err := e.svc.Feed(ctx, viewer, ""); err == nil {
		t.Fatal("feed shown while blocks are unavailable")
	}
	if err := e.svc.RespondToSos(ctx, viewer, v.ID); err == nil {
		t.Fatal("respond allowed while blocks are unavailable")
	}
}

type failingBlocks struct{}

func (failingBlocks) IsBlocked(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, errors.New("membership down")
}

func TestMaskPhone(t *testing.T) {
	for in, want := range map[string]string{
		"+70000000001":   "+7 *** *** 00 01",
		"+77001234567":   "+7 *** *** 45 67",
		"not a number":   "***",
		"+4900000012345": "+4 *** *** 23 45",
	} {
		if got := model.MaskPhone(in); got != want {
			t.Errorf("MaskPhone(%q) = %q, want %q", in, got, want)
		}
	}
}
