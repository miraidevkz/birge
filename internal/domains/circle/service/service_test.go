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

	"birge/internal/domains/circle/circletest"
	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/service"
	"birge/internal/platform/auth"
)

const (
	fakeName = "Example"
	fakeNeed = "autism"
)

type env struct {
	store   *circletest.MemoryStore
	members *circletest.FakeMembers
	clock   *circletest.Clock
	svc     *service.Service
	logBuf  *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := circletest.NewMemoryStore()
	members := circletest.NewFakeMembers()
	clk := &circletest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	buf := &bytes.Buffer{}
	e := &env{store: store, members: members, clock: clk, logBuf: buf, svc: &service.Service{
		Store: store, Members: members, Blocks: members, Clock: clk,
		Logger: slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Settings: service.Settings{
			RequestTTL: 7 * 24 * time.Hour, MatchInterval: time.Hour,
			AskMetAfter: 3 * 24 * time.Hour, RematchCooldown: 30 * 24 * time.Hour,
		},
	}}
	t.Cleanup(func() { assertNoPersonalData(t, e) })
	return e
}

func assertNoPersonalData(t *testing.T, e *env) {
	t.Helper()
	hay := e.logBuf.String()
	for _, bad := range []string{fakeName, fakeNeed} {
		if strings.Contains(hay, bad) {
			t.Errorf("log leaked %q: %s", bad, hay)
		}
	}
	for _, ev := range e.store.Events() {
		raw, err := json.Marshal(ev.Data)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		for _, bad := range []string{fakeName, fakeNeed} {
			if strings.Contains(s, bad) {
				t.Errorf("event %s leaked %q: %s", ev.Type, bad, s)
			}
		}
	}
}

func (e *env) member(verified bool, district *uuid.UUID, year int, needs ...string) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{
		Verified: verified, DistrictID: district, Lang: "ru", FirstName: fakeName,
		Children: []model.Child{{BirthYear: year, Needs: needs}},
	})
	return p
}

func key() uuid.UUID { return uuid.Must(uuid.NewV7()) }

func TestCreateTeaRequestRules(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	verified := e.member(true, &d, 2018, fakeNeed)
	unverified := e.member(false, &d, 2018, fakeNeed)

	if _, err := e.svc.CreateTeaRequest(ctx, unverified, key(), model.FormatMeeting, model.Preferences{}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unverified: %v", err)
	}
	r, err := e.svc.CreateTeaRequest(ctx, verified, key(), model.FormatMeeting, model.Preferences{SameDistrict: true})
	if err != nil || r.Status != model.RequestOpen {
		t.Fatalf("create: %+v %v", r, err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, verified, key(), model.FormatCall, model.Preferences{}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("second open: %v", err)
	}
	idem := key()
	first, err := e.svc.CreateTeaRequest(ctx, e.member(true, &d, 2019), idem, model.FormatCall, model.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.CreateTeaRequest(ctx, auth.Principal{IdentityID: uuid.New(), MemberID: first.MemberID}, idem, model.FormatCall, model.Preferences{})
	if err != nil || again.ID != first.ID {
		t.Fatalf("idempotent: %+v %v", again, err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, auth.Principal{Staff: true, IdentityID: uuid.New()}, key(), model.FormatMeeting, model.Preferences{}); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("staff: %v", err)
	}
}

func TestMatchingAndStateMachine(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d, 2018, fakeNeed)
	b := e.member(true, &d, 2019, fakeNeed)
	c := e.member(true, &d, 2018, "speech")

	ra, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	rb, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatMeeting, model.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, c, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}

	tx, commit := e.store.Tx()
	if err := e.svc.HandleMatchRun(ctx, tx); err != nil {
		t.Fatal(err)
	}
	commit()

	matches, err := e.svc.ListMyMatches(ctx, a)
	if err != nil || len(matches) != 1 {
		t.Fatalf("matches for a: %v %v", matches, err)
	}
	m := matches[0]
	if m.OtherDisplayName != fakeName || m.Status != model.MatchProposed {
		t.Fatalf("card=%+v", m)
	}
	if len(m.SharedNeeds) != 1 || m.SharedNeeds[0] != fakeNeed {
		t.Fatalf("shared=%v", m.SharedNeeds)
	}
	deliveries := e.store.Deliveries()
	if len(deliveries) != 2 {
		t.Fatalf("proposed deliveries=%d", len(deliveries))
	}
	for _, d := range deliveries {
		if d.Template != model.TemplateMatchProposed || d.Class != model.ClassNormal {
			t.Fatalf("delivery=%+v", d)
		}
	}

	// One accept keeps proposed.
	card, err := e.svc.AnswerMatch(ctx, a, m.ID, true)
	if err != nil || card.Status != model.MatchProposed {
		t.Fatalf("one accept: %+v %v", card, err)
	}
	// Second accept → accepted + event + AskMet timer.
	card, err = e.svc.AnswerMatch(ctx, b, m.ID, true)
	if err != nil || card.Status != model.MatchAccepted {
		t.Fatalf("two accepts: %+v %v", card, err)
	}
	accepted := e.store.EventsOfType(model.EventMatchAccepted)
	if len(accepted) != 1 {
		t.Fatalf("MatchAccepted events=%d", len(accepted))
	}
	var askMet bool
	for _, tm := range e.store.ActiveTimers() {
		if tm.Kind == model.TimerAskMet && tm.SubjectID == m.ID {
			askMet = true
		}
	}
	if !askMet {
		t.Fatal("expected AskMet timer")
	}

	tx, commit = e.store.Tx()
	if err := e.svc.HandleAskMet(ctx, tx, m.ID); err != nil {
		t.Fatal(err)
	}
	commit()
	var askN int
	for _, d := range e.store.Deliveries() {
		if d.Template == model.TemplateAskMet {
			askN++
		}
	}
	if askN != 2 {
		t.Fatalf("ask_met deliveries=%d", askN)
	}

	if _, err := e.svc.ConfirmMet(ctx, a, m.ID, true); err != nil {
		t.Fatal(err)
	}
	card, err = e.svc.ConfirmMet(ctx, b, m.ID, true)
	if err != nil || card.Status != model.MatchMet {
		t.Fatalf("met: %+v %v", card, err)
	}
	est := e.store.EventsOfType(model.EventAcquaintanceEstablished)
	if len(est) != 1 {
		t.Fatalf("AcquaintanceEstablished=%d", len(est))
	}
	ok, err := e.svc.Met(ctx, a.MemberID, b.MemberID)
	if err != nil || !ok {
		t.Fatalf("Met=%v %v", ok, err)
	}
	ok, err = e.svc.Met(ctx, b.MemberID, a.MemberID)
	if err != nil || !ok {
		t.Fatalf("Met symmetric=%v %v", ok, err)
	}
	list, err := e.svc.ListAcquaintances(ctx, a)
	if err != nil || len(list) != 1 || list[0].MemberID != b.MemberID {
		t.Fatalf("list=%v %v", list, err)
	}

	// Decline path for another pair: rematch c with whoever is left — create new requests.
	_ = rb
	_ = ra
}

func TestDeclineAndNotMet(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d, 2018, fakeNeed)
	b := e.member(true, &d, 2019, fakeNeed)
	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatCall, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatCall, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	tx, commit := e.store.Tx()
	if err := e.svc.HandleMatchRun(ctx, tx); err != nil {
		t.Fatal(err)
	}
	commit()
	matches, _ := e.svc.ListMyMatches(ctx, a)
	m := matches[0]
	if _, err := e.svc.AnswerMatch(ctx, a, m.ID, false); err != nil {
		t.Fatal(err)
	}
	card, err := e.svc.AnswerMatch(ctx, b, m.ID, true)
	if err != nil || card.Status != model.MatchDeclined {
		t.Fatalf("decline: %+v %v", card, err)
	}
}

func TestBlockedNeverMatched(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d, 2018, fakeNeed)
	b := e.member(true, &d, 2019, fakeNeed)
	e.members.Block(a.MemberID, b.MemberID)
	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	tx, commit := e.store.Tx()
	if err := e.svc.HandleMatchRun(ctx, tx); err != nil {
		t.Fatal(err)
	}
	commit()
	matches, err := e.svc.ListMyMatches(ctx, a)
	if err != nil || len(matches) != 0 {
		t.Fatalf("blocked matches=%v err=%v", matches, err)
	}
}

func TestCancelAndExpire(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d, 2018)
	r, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelTeaRequest(ctx, a, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelTeaRequest(ctx, a, r.ID); err != nil {
		t.Fatal(err)
	}
	stored, ok := e.store.Request(r.ID)
	if !ok || stored.Status != model.RequestCancelled {
		t.Fatalf("stored=%+v", stored)
	}

	r2, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{})
	if err != nil {
		t.Fatal(err)
	}
	tx, commit := e.store.Tx()
	if err := e.svc.HandleRequestExpire(ctx, tx, r2.ID); err != nil {
		t.Fatal(err)
	}
	commit()
	stored, _ = e.store.Request(r2.ID)
	if stored.Status != model.RequestExpired {
		t.Fatalf("status=%s", stored.Status)
	}
}

func TestSetRoomID(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d, 2018, fakeNeed)
	b := e.member(true, &d, 2019, fakeNeed)
	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	tx, commit := e.store.Tx()
	_ = e.svc.HandleMatchRun(ctx, tx)
	commit()
	matches, _ := e.svc.ListMyMatches(ctx, a)
	room := uuid.Must(uuid.NewV7())
	if err := e.svc.SetRoomID(ctx, matches[0].ID, room); err != nil {
		t.Fatal(err)
	}
	m, ok := e.store.Match(matches[0].ID)
	if !ok || m.RoomID == nil || *m.RoomID != room {
		t.Fatalf("room=%+v", m)
	}
}

func TestBlocksFailClosed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := uuid.Must(uuid.NewV7())
	a := e.member(true, &d, 2018)
	b := e.member(true, &d, 2019)
	e.members.Fail = errors.New("membership down")
	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{}); err == nil {
		// Create needs profile first — Fail hits Profiles.
		t.Fatal("expected profile failure")
	}
	e.members.Fail = nil
	if _, err := e.svc.CreateTeaRequest(ctx, a, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateTeaRequest(ctx, b, key(), model.FormatMeeting, model.Preferences{}); err != nil {
		t.Fatal(err)
	}
	e.members.Fail = errors.New("blocks down")
	tx, commit := e.store.Tx()
	err := e.svc.HandleMatchRun(ctx, tx)
	commit()
	if err == nil {
		t.Fatal("expected blocks failure")
	}
}
