package service_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/safety/crisis"
	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/safetytest"
	"birge/internal/domains/safety/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/crypto"
)

// Clearly fake personal data: never real numbers or names.
const (
	fakeName      = "Example Member"
	fakePhone     = "+70000000001"
	fakeDutyPhone = "+70000000002"
	fakeNote      = "example note text"
)

type env struct {
	svc   *service.Service
	store *safetytest.MemoryStore
	dir   *safetytest.FakeDirectory
	clock *safetytest.Clock
	logs  *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	logs := &bytes.Buffer{}
	e := &env{
		store: safetytest.NewMemoryStore(),
		dir:   &safetytest.FakeDirectory{Contacts: map[uuid.UUID]model.Contact{}, Districts: map[uuid.UUID]uuid.UUID{}},
		clock: &safetytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		logs:  logs,
	}
	e.svc = &service.Service{
		Store:     e.store,
		Sealer:    &safetytest.FakeSealer{},
		Directory: e.dir,
		Clock:     e.clock,
		Logger:    slog.New(slog.NewTextHandler(&lockedWriter{w: logs}, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Settings: service.Settings{
			CrisisPhones: []model.CrisisPhone{{Number: "000", Label: "Example line"}},
		},
	}
	t.Cleanup(func() {
		// No personal data may ever reach the logs.
		out := logs.String()
		for _, secret := range []string{fakeName, fakePhone, fakeDutyPhone, fakeNote} {
			if strings.Contains(out, secret) {
				t.Errorf("logs contain personal data %q", secret)
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

// member is a member session as the middleware builds it: identity and member IDs.
func member() auth.Principal { return auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()} }

func staff(roles ...string) auth.Principal {
	return auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: roles}
}

func (e *env) open(ctx context.Context, t *testing.T, memberID uuid.UUID) uuid.UUID {
	t.Helper()
	id, already, err := e.svc.OpenEscalation(ctx, memberID, model.SourceAssistant)
	if err != nil || already {
		t.Fatalf("OpenEscalation: already=%v err=%v", already, err)
	}
	return id
}

func TestOpenEscalationAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := uuid.New()
	district := uuid.New()
	e.dir.Districts[m] = district

	id := e.open(ctx, t, m)

	esc, ok := e.store.Escalation(id)
	if !ok || esc.Status != model.StatusOpen || esc.Source != model.SourceAssistant || esc.DistrictID == nil || *esc.DistrictID != district {
		t.Fatalf("escalation = %+v", esc)
	}
	opened := e.store.EventsOfType(model.EventEscalationOpened)
	if len(opened) != 1 {
		t.Fatalf("opened events = %d", len(opened))
	}
	if d := opened[0].Data.(model.EscalationOpenedData); d.EscalationID != id || d.MemberID != m || d.Source != model.SourceAssistant {
		t.Fatalf("opened data = %+v", d)
	}
	alerts := e.store.EventsOfType(model.EventDeliveryRequested)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %d", len(alerts))
	}
	a := alerts[0].Data.(model.DeliveryRequestedData)
	if a.Recipient.StaffRole != model.RecipientPsychologist || a.Class != model.ClassCritical ||
		a.Template != model.TemplateEscalationOpened || a.Params["escalation_id"] != id.String() || len(a.Params) != 1 ||
		a.RequestID != model.DeliveryRequestID(id, model.TemplateEscalationOpened) {
		t.Fatalf("alert = %+v", a)
	}
	tm, ok := e.store.Timer(id)
	if !ok || tm.Cancelled || !tm.DueAt.Equal(e.clock.Now().Add(15*time.Minute)) {
		t.Fatalf("timer = %+v ok=%v", tm, ok)
	}

	again, already, err := e.svc.OpenEscalation(ctx, m, model.SourceSOS)
	if err != nil || !already || again != id {
		t.Fatalf("second open: id=%v already=%v err=%v", again, already, err)
	}
	if n := len(e.store.Events()); n != 2 {
		t.Fatalf("events after repeat = %d, want 2", n)
	}
}

func TestOpenEscalationConcurrentFake(t *testing.T) {
	e := newEnv(t)
	m := uuid.New()
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 20)
	errs := make([]error, 20)
	for i := range 20 {
		wg.Go(func() {
			ids[i], _, errs[i] = e.svc.OpenEscalation(context.Background(), m, model.SourceMenu)
		})
	}
	wg.Wait()
	for i := range 20 {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("call %d: id=%v err=%v", i, ids[i], errs[i])
		}
	}
	if e.store.Escalations() != 1 || len(e.store.EventsOfType(model.EventEscalationOpened)) != 1 {
		t.Fatalf("escalations=%d", e.store.Escalations())
	}
}

func TestOpenEscalationRollsBackOnFailure(t *testing.T) {
	for _, method := range []string{"Publish", "ScheduleBackup", "InsertEscalation"} {
		t.Run(method, func(t *testing.T) {
			e := newEnv(t)
			e.store.FailOn = func(m string) error {
				if m == method {
					return errors.New("injected")
				}
				return nil
			}
			if _, _, err := e.svc.OpenEscalation(context.Background(), uuid.New(), model.SourceAssistant); err == nil {
				t.Fatal("no error")
			}
			if e.store.Escalations() != 0 || len(e.store.Events()) != 0 {
				t.Fatalf("partial commit: escalations=%d events=%d", e.store.Escalations(), len(e.store.Events()))
			}
		})
	}
}

func TestOpenEscalationValidationAndDirectoryFailure(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	if _, _, err := e.svc.OpenEscalation(ctx, uuid.Nil, model.SourceMenu); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil member: %v", err)
	}
	if _, _, err := e.svc.OpenEscalation(ctx, uuid.New(), "chat"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad source: %v", err)
	}
	// The directory is optional: an escalation opens without a district.
	e.dir.Err = errors.New("membership down")
	id := e.open(ctx, t, uuid.New())
	if esc, _ := e.store.Escalation(id); esc.DistrictID != nil {
		t.Fatal("district set despite directory failure")
	}
	// Without a directory at all.
	e.svc.Directory = nil
	e.open(ctx, t, uuid.New())
}

func TestNewEscalationAfterClose(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := uuid.New()
	id := e.open(ctx, t, m)
	psy := staff(model.RolePsychologist)
	if _, err := e.svc.TakeEscalation(ctx, psy, id); err != nil {
		t.Fatal(err)
	}
	// Taken still counts as active.
	if again, already, _ := e.svc.OpenEscalation(ctx, m, model.SourceMenu); !already || again != id {
		t.Fatal("taken escalation not returned")
	}
	if _, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeContacted, nil); err != nil {
		t.Fatal(err)
	}
	next := e.open(ctx, t, m)
	if next == id {
		t.Fatal("closed escalation reused")
	}
}

func TestStaffRoles(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		p    auth.Principal
		want error
	}{
		{"anonymous", auth.Principal{}, model.ErrUnauthorized},
		{"member", member(), model.ErrForbidden},
		{"staff without roles", staff(), model.ErrForbidden},
		{"admin only", staff(model.RoleAdmin), model.ErrForbidden},
		{"unknown role", staff("support"), model.ErrForbidden},
		{"psychologist", staff(model.RolePsychologist), nil},
		{"moderator", staff("support", model.RoleModerator), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			id := e.open(ctx, t, uuid.New())
			if _, err := e.svc.ListEscalations(ctx, tt.p, "", ""); !errors.Is(err, tt.want) {
				t.Fatalf("list: %v, want %v", err, tt.want)
			}
			_, err := e.svc.TakeEscalation(ctx, tt.p, id)
			if !errors.Is(err, tt.want) {
				t.Fatalf("take: %v, want %v", err, tt.want)
			}
		})
	}
	// Roster changes are admin only.
	for _, tt := range []struct {
		p    auth.Principal
		want error
	}{
		{auth.Principal{}, model.ErrUnauthorized},
		{member(), model.ErrForbidden},
		{staff(model.RolePsychologist), model.ErrForbidden},
		{staff(model.RoleModerator), model.ErrForbidden},
	} {
		e := newEnv(t)
		if _, err := e.svc.AddDutyShift(ctx, tt.p, validShift(e)); !errors.Is(err, tt.want) {
			t.Fatalf("add shift by %+v: %v", tt.p.Roles, err)
		}
		if err := e.svc.CancelDutyShift(ctx, tt.p, uuid.New()); !errors.Is(err, tt.want) {
			t.Fatalf("cancel shift: %v", err)
		}
	}
	// Members cannot read the roster; staff with a queue role or admin can.
	e := newEnv(t)
	from := e.clock.Now()
	if _, err := e.svc.DutyRoster(ctx, member(), from, from.Add(time.Hour)); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("member roster: %v", err)
	}
	for _, r := range []string{model.RoleAdmin, model.RolePsychologist, model.RoleModerator} {
		if _, err := e.svc.DutyRoster(ctx, staff(r), from, from.Add(time.Hour)); err != nil {
			t.Fatalf("%s roster: %v", r, err)
		}
	}
}

func TestTakeEscalation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.open(ctx, t, uuid.New())
	psy := staff(model.RolePsychologist)

	got, err := e.svc.TakeEscalation(ctx, psy, id)
	if err != nil || got.Status != model.StatusTaken || !got.TakenByStaff(psy.IdentityID) {
		t.Fatalf("take: %+v %v", got, err)
	}
	if tm, _ := e.store.Timer(id); !tm.Cancelled {
		t.Fatal("backup timer not cancelled")
	}
	taken := e.store.EventsOfType(model.EventEscalationTaken)
	if len(taken) != 1 || taken[0].Data.(model.EscalationTakenData).TakenBy != psy.IdentityID {
		t.Fatalf("taken events = %+v", taken)
	}
	audit := e.store.Audit()
	if len(audit) != 1 || audit[0].Action != model.AuditTake || audit[0].ActorID != psy.IdentityID ||
		audit[0].ActorRole != model.RolePsychologist || audit[0].ObjectID != id || audit[0].ObjectType != model.AuditObjectEscalation {
		t.Fatalf("audit = %+v", audit)
	}

	// Same person again: unchanged, no second event or audit entry.
	if again, err := e.svc.TakeEscalation(ctx, psy, id); err != nil || again.ID != id {
		t.Fatalf("retake: %v", err)
	}
	if len(e.store.Audit()) != 1 || len(e.store.EventsOfType(model.EventEscalationTaken)) != 1 {
		t.Fatal("retake repeated side effects")
	}
	// Someone else: conflict.
	if _, err := e.svc.TakeEscalation(ctx, staff(model.RoleModerator), id); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("other staff: %v", err)
	}
	if _, err := e.svc.TakeEscalation(ctx, psy, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestRevealContactRules(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := uuid.New()
	e.dir.Contacts[m] = model.Contact{DisplayName: fakeName, Lang: "kk", Phone: fakePhone}
	id := e.open(ctx, t, m)
	psy := staff(model.RolePsychologist)
	other := staff(model.RolePsychologist)

	if _, err := e.svc.RevealContact(ctx, psy, id); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("before take: %v", err)
	}
	if _, err := e.svc.TakeEscalation(ctx, psy, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RevealContact(ctx, other, id); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("other staff: %v", err)
	}
	if _, err := e.svc.RevealContact(ctx, member(), id); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("member: %v", err)
	}
	if e.dir.ContactCalls != 0 {
		t.Fatal("directory read before authorization")
	}
	c, err := e.svc.RevealContact(ctx, psy, id)
	if err != nil || c.DisplayName != fakeName || c.Phone != fakePhone || c.Lang != "kk" {
		t.Fatalf("reveal: %+v %v", c, err)
	}
	var reveals int
	for _, a := range e.store.Audit() {
		if a.Action == model.AuditRevealContact {
			reveals++
			if a.ActorID != psy.IdentityID || a.ObjectID != id {
				t.Fatalf("reveal audit = %+v", a)
			}
		}
	}
	if reveals != 1 {
		t.Fatalf("reveal audit entries = %d", reveals)
	}
	if _, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeContacted, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RevealContact(ctx, psy, id); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("after close: %v", err)
	}
	if _, err := e.svc.RevealContact(ctx, psy, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestCloseEscalation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := uuid.New()
	id := e.open(ctx, t, m)
	psy := staff(model.RolePsychologist)

	note := fakeNote
	if _, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeContacted, &note); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("close open escalation: %v", err)
	}
	if _, err := e.svc.TakeEscalation(ctx, psy, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CloseEscalation(ctx, staff(model.RolePsychologist), id, model.OutcomeContacted, nil); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("close by other: %v", err)
	}
	if _, err := e.svc.CloseEscalation(ctx, psy, id, "solved", nil); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad outcome: %v", err)
	}
	long := strings.Repeat("x", model.MaxNoteLen+1)
	if _, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeContacted, &long); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("long note: %v", err)
	}

	got, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeReferred, &note)
	if err != nil || got.Status != model.StatusClosed || got.Outcome == nil || *got.Outcome != model.OutcomeReferred {
		t.Fatalf("close: %+v %v", got, err)
	}
	// The note is sealed under the member's key and bound to this row.
	stored, _ := e.store.Escalation(id)
	if !bytes.HasPrefix(stored.Note, []byte("sealed|"+m.String()+"|")) {
		t.Fatal("note not sealed under the member's key")
	}
	plain, err := (&safetytest.FakeSealer{}).Open(ctx, m, stored.Note, crypto.AssociatedData(model.FieldEscalationNote, id))
	if err != nil || string(plain) != fakeNote {
		t.Fatalf("sealed note does not open for the member and row: %v", err)
	}
	closed := e.store.EventsOfType(model.EventEscalationClosed)
	if len(closed) != 1 || closed[0].Data.(model.EscalationClosedData).Outcome != model.OutcomeReferred {
		t.Fatalf("closed events = %+v", closed)
	}
	if a := e.store.Audit(); a[len(a)-1].Action != model.AuditClose {
		t.Fatalf("audit = %+v", a)
	}
	if _, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeReferred, nil); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("close twice: %v", err)
	}
}

func TestCloseWithoutNoteAndSealerFailure(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	id := e.open(ctx, t, uuid.New())
	psy := staff(model.RoleModerator)
	if _, err := e.svc.TakeEscalation(ctx, psy, id); err != nil {
		t.Fatal(err)
	}
	e.svc.Sealer = &safetytest.FakeSealer{Err: errors.New("kek down")}
	note := fakeNote
	if _, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeNoAnswer, &note); err == nil {
		t.Fatal("sealer failure ignored")
	}
	if esc, _ := e.store.Escalation(id); esc.Status != model.StatusTaken {
		t.Fatal("escalation closed despite sealer failure")
	}
	blank := "  "
	got, err := e.svc.CloseEscalation(ctx, psy, id, model.OutcomeNoAnswer, &blank)
	if err != nil || got.Note != nil {
		t.Fatalf("blank note: %+v %v", got, err)
	}
}

func TestBackupTimer(t *testing.T) {
	ctx := context.Background()

	t.Run("still open alerts the backup", func(t *testing.T) {
		e := newEnv(t)
		id := e.open(ctx, t, uuid.New())
		e.clock.Advance(15 * time.Minute)
		tx, commit := e.store.Tx()
		if err := e.svc.HandleEscalationBackup(ctx, tx, id); err != nil {
			t.Fatal(err)
		}
		commit()
		un := e.store.EventsOfType(model.EventEscalationUnattended)
		if len(un) != 1 || un[0].Data.(model.EscalationUnattendedData).MinutesOpen != 15 {
			t.Fatalf("unattended = %+v", un)
		}
		alerts := e.store.EventsOfType(model.EventDeliveryRequested)
		if len(alerts) != 2 {
			t.Fatalf("alerts = %d", len(alerts))
		}
		b := alerts[1].Data.(model.DeliveryRequestedData)
		if b.Recipient.StaffRole != model.RecipientPsychologistBackup || b.Class != model.ClassCritical ||
			b.Template != model.TemplateEscalationUnattended || b.Params["escalation_id"] != id.String() {
			t.Fatalf("backup alert = %+v", b)
		}
	})

	for _, tc := range []struct {
		name string
		prep func(e *env, id uuid.UUID)
	}{
		{"taken does nothing", func(e *env, id uuid.UUID) {
			if _, err := e.svc.TakeEscalation(ctx, staff(model.RolePsychologist), id); err != nil {
				t.Fatal(err)
			}
		}},
		{"closed does nothing", func(e *env, id uuid.UUID) {
			p := staff(model.RolePsychologist)
			if _, err := e.svc.TakeEscalation(ctx, p, id); err != nil {
				t.Fatal(err)
			}
			if _, err := e.svc.CloseEscalation(ctx, p, id, model.OutcomeNotACrisis, nil); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			id := e.open(ctx, t, uuid.New())
			tc.prep(e, id)
			before := len(e.store.Events())
			e.clock.Advance(15 * time.Minute)
			tx, commit := e.store.Tx()
			if err := e.svc.HandleEscalationBackup(ctx, tx, id); err != nil {
				t.Fatal(err)
			}
			commit()
			if len(e.store.Events()) != before {
				t.Fatal("handler published for a taken or closed escalation")
			}
		})
	}

	t.Run("unknown escalation is a no-op", func(t *testing.T) {
		e := newEnv(t)
		tx, commit := e.store.Tx()
		defer commit()
		if err := e.svc.HandleEscalationBackup(ctx, tx, uuid.New()); err != nil {
			t.Fatal(err)
		}
	})
}

func validShift(e *env) model.NewDutyShift {
	now := e.clock.Now()
	return model.NewDutyShift{
		ID: uuid.Must(uuid.NewV7()), StaffID: uuid.New(), Role: model.RolePsychologist,
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(8 * time.Hour), Phone: fakeDutyPhone,
	}
}

func TestDutyRosterAndResolveStaffRole(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := staff(model.RoleAdmin)

	if _, err := e.svc.ResolveStaffRole(ctx, model.RecipientPsychologist); !errors.Is(err, model.ErrNoDutyCoverage) {
		t.Fatalf("empty roster: %v", err)
	}
	if !strings.Contains(e.logs.String(), "NoDutyCoverage") {
		t.Fatal("NoDutyCoverage not logged")
	}

	primary := validShift(e)
	got, err := e.svc.AddDutyShift(ctx, admin, primary)
	if err != nil || got.ID != primary.ID || !bytes.HasPrefix(got.Phone, []byte("sealed|"+primary.StaffID.String()+"|")) {
		t.Fatalf("add: %+v %v", got, err)
	}
	// Same Idempotency-Key: one effect.
	if again, err := e.svc.AddDutyShift(ctx, admin, primary); err != nil || again.ID != primary.ID {
		t.Fatalf("repeat add: %v", err)
	}
	var adds int
	for _, a := range e.store.Audit() {
		if a.Action == model.AuditShiftAdd {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("add audit entries = %d", adds)
	}

	backup := validShift(e)
	backup.Backup = true
	backup.Phone = "+70000000003"
	if _, err := e.svc.AddDutyShift(ctx, admin, backup); err != nil {
		t.Fatal(err)
	}
	future := validShift(e)
	future.StartsAt, future.EndsAt = e.clock.Now().Add(24*time.Hour), e.clock.Now().Add(32*time.Hour)
	if _, err := e.svc.AddDutyShift(ctx, admin, future); err != nil {
		t.Fatal(err)
	}

	rs, err := e.svc.ResolveStaffRole(ctx, model.RecipientPsychologist)
	if err != nil || len(rs) != 1 || rs[0].Ref != primary.ID || rs[0].PhoneE164 != fakeDutyPhone || rs[0].Lang != "ru" {
		t.Fatalf("psychologist: %+v %v", rs, err)
	}
	rs, err = e.svc.ResolveStaffRole(ctx, model.RecipientPsychologistBackup)
	if err != nil || len(rs) != 1 || rs[0].Ref != backup.ID || rs[0].PhoneE164 != "+70000000003" {
		t.Fatalf("backup: %+v %v", rs, err)
	}
	if _, err := e.svc.ResolveStaffRole(ctx, model.RecipientModerator); !errors.Is(err, model.ErrNoDutyCoverage) {
		t.Fatalf("moderator: %v", err)
	}
	if _, err := e.svc.ResolveStaffRole(ctx, "janitor"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad role: %v", err)
	}

	roster, err := e.svc.DutyRoster(ctx, staff(model.RolePsychologist), e.clock.Now(), e.clock.Now().Add(48*time.Hour))
	if err != nil || len(roster) != 3 {
		t.Fatalf("roster: %d %v", len(roster), err)
	}
	if _, err := e.svc.DutyRoster(ctx, admin, e.clock.Now(), e.clock.Now()); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("empty window: %v", err)
	}
	if _, err := e.svc.DutyRoster(ctx, admin, e.clock.Now(), e.clock.Now().Add(100*24*time.Hour)); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("long window: %v", err)
	}

	// A started shift cannot be cancelled; a future one can, once.
	if err := e.svc.CancelDutyShift(ctx, admin, primary.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("cancel started: %v", err)
	}
	if err := e.svc.CancelDutyShift(ctx, admin, future.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelDutyShift(ctx, admin, future.ID); err != nil {
		t.Fatalf("cancel twice: %v", err)
	}
	if err := e.svc.CancelDutyShift(ctx, admin, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("cancel unknown: %v", err)
	}
	roster, _ = e.svc.DutyRoster(ctx, admin, e.clock.Now(), e.clock.Now().Add(48*time.Hour))
	if len(roster) != 2 {
		t.Fatalf("roster after cancel = %d", len(roster))
	}

	// The shift ends: no coverage.
	e.clock.Advance(9 * time.Hour)
	if _, err := e.svc.ResolveStaffRole(ctx, model.RecipientPsychologist); !errors.Is(err, model.ErrNoDutyCoverage) {
		t.Fatalf("after shift: %v", err)
	}
}

func TestResolveStaffRoleSkipsUnreadablePhone(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	now := e.clock.Now()
	good := validShift(e)
	if _, err := e.svc.AddDutyShift(ctx, staff(model.RoleAdmin), good); err != nil {
		t.Fatal(err)
	}
	// A shift whose phone was sealed for another row does not open.
	e.store.PutShift(model.DutyShift{
		ID: uuid.New(), StaffID: uuid.New(), Role: model.RolePsychologist,
		StartsAt: now.Add(-2 * time.Hour), EndsAt: now.Add(time.Hour), Phone: []byte("sealed|garbage"),
	})
	rs, err := e.svc.ResolveStaffRole(ctx, model.RecipientPsychologist)
	if err != nil || len(rs) != 1 || rs[0].Ref != good.ID {
		t.Fatalf("resolve: %+v %v", rs, err)
	}
}

func TestAddDutyShiftValidation(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := staff(model.RoleAdmin)
	now := e.clock.Now()
	tests := map[string]func(s *model.NewDutyShift){
		"no id":       func(s *model.NewDutyShift) { s.ID = uuid.Nil },
		"no staff":    func(s *model.NewDutyShift) { s.StaffID = uuid.Nil },
		"bad role":    func(s *model.NewDutyShift) { s.Role = model.RoleAdmin },
		"bad phone":   func(s *model.NewDutyShift) { s.Phone = "8 700 000" },
		"reversed":    func(s *model.NewDutyShift) { s.EndsAt = s.StartsAt.Add(-time.Minute) },
		"too long":    func(s *model.NewDutyShift) { s.EndsAt = s.StartsAt.Add(8 * 24 * time.Hour) },
		"in the past": func(s *model.NewDutyShift) { s.StartsAt, s.EndsAt = now.Add(-3*time.Hour), now.Add(-time.Hour) },
	}
	for name, mutate := range tests {
		s := validShift(e)
		mutate(&s)
		if _, err := e.svc.AddDutyShift(ctx, admin, s); !errors.Is(err, model.ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestListEscalationsPagination(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	for range 55 {
		e.open(ctx, t, uuid.New())
		e.clock.Advance(time.Second)
	}
	p := staff(model.RoleModerator)
	page, err := e.svc.ListEscalations(ctx, p, model.StatusOpen, "")
	if err != nil || len(page.Items) != 50 || page.NextCursor == "" {
		t.Fatalf("page 1: %d %q %v", len(page.Items), page.NextCursor, err)
	}
	if !page.Items[0].OpenedAt.After(page.Items[49].OpenedAt) {
		t.Fatal("not newest first")
	}
	page2, err := e.svc.ListEscalations(ctx, p, model.StatusOpen, page.NextCursor)
	if err != nil || len(page2.Items) != 5 || page2.NextCursor != "" {
		t.Fatalf("page 2: %d %q %v", len(page2.Items), page2.NextCursor, err)
	}
	seen := map[uuid.UUID]bool{}
	for _, it := range append(page.Items, page2.Items...) {
		if seen[it.ID] {
			t.Fatal("duplicate across pages")
		}
		seen[it.ID] = true
	}
	if page, _ := e.svc.ListEscalations(ctx, p, model.StatusClosed, ""); len(page.Items) != 0 {
		t.Fatal("status filter ignored")
	}
	for _, bad := range []string{"%%%", "bm90LWEtY3Vyc29y"} {
		if _, err := e.svc.ListEscalations(ctx, p, "", bad); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("cursor %q: %v", bad, err)
		}
	}
	if _, err := e.svc.ListEscalations(ctx, p, "pending", ""); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad status: %v", err)
	}
}

func TestRequestHumanHelp(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m := member()
	res, err := e.svc.RequestHumanHelp(ctx, m, model.SourceMenu)
	if err != nil || res.EscalationID == uuid.Nil || len(res.CrisisPhones) != 1 || res.CrisisPhones[0].Number != "000" {
		t.Fatalf("help: %+v %v", res, err)
	}
	again, err := e.svc.RequestHumanHelp(ctx, m, model.SourceMenu)
	if err != nil || again.EscalationID != res.EscalationID {
		t.Fatalf("repeat: %v", err)
	}
	if _, err := e.svc.RequestHumanHelp(ctx, staff(model.RolePsychologist), model.SourceMenu); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("staff: %v", err)
	}
	if _, err := e.svc.RequestHumanHelp(ctx, auth.Principal{}, model.SourceMenu); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("anonymous: %v", err)
	}
}

func TestDetectReexport(t *testing.T) {
	e := newEnv(t)
	if e.svc.Detect("хочу умереть", "ru").Crisis {
		t.Fatal("nil lexicon detected")
	}
	if !strings.Contains(e.logs.String(), "LexiconMissing") {
		t.Fatal("missing lexicon not logged")
	}
	lex, err := crisis.LoadDir(filepath.Join("..", "crisis", "testdata", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.Lexicon = lex
	r := e.svc.Detect("Өлгім келеді", "kk")
	if !r.Crisis || r.Category != "suicide" {
		t.Fatalf("detect = %+v", r)
	}
	if strings.Contains(e.logs.String(), "келеді") {
		t.Fatal("message text logged")
	}
}
