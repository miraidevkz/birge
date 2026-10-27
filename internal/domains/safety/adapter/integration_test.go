package adapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/safety/adapter"
	"birge/internal/domains/safety/model"
	"birge/internal/domains/safety/port"
	"birge/internal/domains/safety/safetytest"
	"birge/internal/domains/safety/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

func newService(store port.Store, clk *safetytest.Clock) *service.Service {
	return &service.Service{
		Store:     store,
		Sealer:    &safetytest.FakeSealer{},
		Directory: &safetytest.FakeDirectory{},
		Clock:     clk,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConcurrentOpenCreatesOneEscalation(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	clk := &safetytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := newService(&adapter.PostgresStore{Pool: pool}, clk)
	memberID := uuid.New()

	const n = 20
	ids := make([]uuid.UUID, n)
	already := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			ids[i], already[i], errs[i] = svc.OpenEscalation(ctx, memberID, model.SourceAssistant)
		})
	}
	close(start)
	wg.Wait()

	created := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("call %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("call %d returned %v, want %v", i, ids[i], ids[0])
		}
		if !already[i] {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created = %d, want 1", created)
	}
	if got := count(t, pool, `SELECT count(*) FROM safety.escalations WHERE member_id = $1`, memberID); got != 1 {
		t.Fatalf("escalation rows = %d", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventEscalationOpened); got != 1 {
		t.Fatalf("opened events = %d", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventDeliveryRequested); got != 1 {
		t.Fatalf("alerts = %d", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.timers WHERE kind = $1 AND subject_id = $2 AND done_at IS NULL`,
		model.TimerEscalationBackup, ids[0]); got != 1 {
		t.Fatalf("timers = %d", got)
	}

	// The alert payload matches the notify.DeliveryRequested contract.
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM platform.outbox WHERE event_type = $1`, model.EventDeliveryRequested).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var alert struct {
		RequestID uuid.UUID         `json:"request_id"`
		Recipient map[string]string `json:"recipient"`
		Class     string            `json:"class"`
		Template  string            `json:"template"`
		Params    map[string]string `json:"params"`
	}
	if err := json.Unmarshal(payload, &alert); err != nil {
		t.Fatal(err)
	}
	if alert.Recipient["staff_role"] != "psychologist" || len(alert.Recipient) != 1 || alert.Class != "critical" ||
		alert.Template != "safety.escalation_opened" || alert.Params["escalation_id"] != ids[0].String() || alert.RequestID == uuid.Nil {
		t.Fatalf("alert = %+v", alert)
	}
}

// failingStore fails one Tx method after the earlier writes of the
// transaction succeeded, to show that nothing is committed half-way.
type failingStore struct {
	inner  *adapter.PostgresStore
	method string
}

func (s *failingStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return s.inner.InTx(ctx, func(tx port.Tx) error {
		return fn(&failingTx{Tx: tx, method: s.method})
	})
}

type failingTx struct {
	port.Tx
	method string
}

var errInjected = errors.New("injected failure")

func (t *failingTx) ScheduleBackup(ctx context.Context, id uuid.UUID, due time.Time) (bool, error) {
	if t.method == "ScheduleBackup" {
		return false, errInjected
	}
	return t.Tx.ScheduleBackup(ctx, id, due)
}

func (t *failingTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	if t.method == "Publish:"+e.Type {
		return errInjected
	}
	return t.Tx.Publish(ctx, now, e)
}

func TestOpenEscalationIsAtomic(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	clk := &safetytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	for _, method := range []string{"ScheduleBackup", "Publish:" + model.EventDeliveryRequested} {
		svc := newService(&failingStore{inner: &adapter.PostgresStore{Pool: pool}, method: method}, clk)
		if _, _, err := svc.OpenEscalation(ctx, uuid.New(), model.SourceSOS); !errors.Is(err, errInjected) {
			t.Fatalf("%s: err = %v", method, err)
		}
	}
	if got := count(t, pool, `SELECT count(*) FROM safety.escalations`); got != 0 {
		t.Fatalf("escalations after failures = %d", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.outbox`); got != 0 {
		t.Fatalf("outbox after failures = %d", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.timers`); got != 0 {
		t.Fatalf("timers after failures = %d", got)
	}
}

func TestTakeCancelsTimerAndUntakenFires(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	clk := &safetytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := newService(&adapter.PostgresStore{Pool: pool}, clk)
	worker := &timers.Worker{
		Pool: pool, Clock: clk, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Interval: time.Second,
		Handlers: (&adapter.TimerHandlers{Service: svc}).Handlers(),
	}

	taken, _, err := svc.OpenEscalation(ctx, uuid.New(), model.SourceAssistant)
	if err != nil {
		t.Fatal(err)
	}
	untaken, _, err := svc.OpenEscalation(ctx, uuid.New(), model.SourceCheckin)
	if err != nil {
		t.Fatal(err)
	}
	psy := auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{model.RolePsychologist}}
	if _, err := svc.TakeEscalation(ctx, psy, taken); err != nil {
		t.Fatal(err)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.timers WHERE subject_id = $1 AND done_at IS NOT NULL AND last_error = 'cancelled'`, taken); got != 1 {
		t.Fatalf("taken escalation timer not cancelled: %d", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.audit_log WHERE action = $1 AND object_id = $2 AND actor_id = $3 AND actor_role = $4`,
		model.AuditTake, taken, psy.IdentityID, model.RolePsychologist); got != 1 {
		t.Fatalf("take audit rows = %d", got)
	}

	// Not due yet.
	if fired, err := worker.RunOnce(ctx); fired || err != nil {
		t.Fatalf("early: fired=%v err=%v", fired, err)
	}
	clk.Advance(15*time.Minute + time.Second)
	if fired, err := worker.RunOnce(ctx); !fired || err != nil {
		t.Fatalf("due: fired=%v err=%v", fired, err)
	}
	if fired, _ := worker.RunOnce(ctx); fired {
		t.Fatal("a second timer fired: the taken escalation's timer was not cancelled")
	}

	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM platform.outbox WHERE event_type = $1`, model.EventEscalationUnattended).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var un model.EscalationUnattendedData
	if err := json.Unmarshal(payload, &un); err != nil {
		t.Fatal(err)
	}
	if un.EscalationID != untaken || un.MinutesOpen != 15 {
		t.Fatalf("unattended = %+v", un)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->'recipient'->>'staff_role' = 'psychologist_backup' AND payload->'params'->>'escalation_id' = $2`,
		model.EventDeliveryRequested, untaken.String()); got != 1 {
		t.Fatalf("backup alerts = %d", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventEscalationUnattended); got != 1 {
		t.Fatalf("unattended events = %d", got)
	}
}

func TestCloseRevealAndRosterPostgres(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	clk := &safetytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	svc := newService(&adapter.PostgresStore{Pool: pool}, clk)
	memberID := uuid.New()
	svc.Directory = &safetytest.FakeDirectory{Contacts: map[uuid.UUID]model.Contact{
		memberID: {DisplayName: "Example Member", Lang: "ru", Phone: "+70000000001"},
	}}

	id, _, err := svc.OpenEscalation(ctx, memberID, model.SourceMenu)
	if err != nil {
		t.Fatal(err)
	}
	psy := auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{model.RolePsychologist}}
	if _, err := svc.TakeEscalation(ctx, psy, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevealContact(ctx, psy, id); err != nil {
		t.Fatal(err)
	}
	note := "example note"
	if _, err := svc.CloseEscalation(ctx, psy, id, model.OutcomeContacted, &note); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{model.AuditTake, model.AuditRevealContact, model.AuditClose} {
		if got := count(t, pool, `SELECT count(*) FROM platform.audit_log WHERE action = $1 AND object_id = $2 AND object_type = $3`,
			action, id, model.AuditObjectEscalation); got != 1 {
			t.Fatalf("%s audit rows = %d", action, got)
		}
	}
	if got := count(t, pool, `SELECT count(*) FROM safety.escalations WHERE id = $1 AND status = 'closed' AND note IS NOT NULL AND position('example note' in convert_from(note, 'UTF8')) > 0 AND position('sealed|' in convert_from(note, 'UTF8')) = 1`, id); got != 1 {
		t.Fatalf("closed row with sealed note = %d", got)
	}
	// After close the member can open a new escalation.
	clk.Advance(time.Minute)
	if next, already, err := svc.OpenEscalation(ctx, memberID, model.SourceMenu); err != nil || already || next == id {
		t.Fatalf("reopen: %v %v %v", next, already, err)
	}

	// Queue pagination against PostgreSQL.
	mod := auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{model.RoleModerator}}
	page, err := svc.ListEscalations(ctx, mod, "", "")
	if err != nil || len(page.Items) != 2 || page.Items[0].Status != model.StatusOpen {
		t.Fatalf("list: %+v %v", page, err)
	}

	// Roster and role resolution.
	admin := auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{model.RoleAdmin}}
	shiftID := uuid.Must(uuid.NewV7())
	in := model.NewDutyShift{
		ID: shiftID, StaffID: uuid.New(), Role: model.RolePsychologist, Backup: true,
		StartsAt: clk.Now().Add(-time.Hour), EndsAt: clk.Now().Add(time.Hour), Phone: "+70000000002",
	}
	if _, err := svc.AddDutyShift(ctx, admin, in); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddDutyShift(ctx, admin, in); err != nil {
		t.Fatalf("idempotent add: %v", err)
	}
	if got := count(t, pool, `SELECT count(*) FROM safety.duty_shifts`); got != 1 {
		t.Fatalf("shifts = %d", got)
	}
	rs, err := svc.ResolveStaffRole(ctx, model.RecipientPsychologistBackup)
	if err != nil || len(rs) != 1 || rs[0].Ref != shiftID || rs[0].PhoneE164 != "+70000000002" {
		t.Fatalf("resolve backup: %+v %v", rs, err)
	}
	if _, err := svc.ResolveStaffRole(ctx, model.RecipientPsychologist); !errors.Is(err, model.ErrNoDutyCoverage) {
		t.Fatalf("resolve primary: %v", err)
	}
	if err := svc.CancelDutyShift(ctx, admin, shiftID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("cancel started: %v", err)
	}
	roster, err := svc.DutyRoster(ctx, admin, clk.Now().Add(-2*time.Hour), clk.Now().Add(2*time.Hour))
	if err != nil || len(roster) != 1 {
		t.Fatalf("roster: %d %v", len(roster), err)
	}
}

func TestEscalationLifecycleConstraint(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	// A closed escalation without a taker or outcome violates the lifecycle check.
	_, err := pool.Exec(ctx, `INSERT INTO safety.escalations (member_id, source, status, opened_at, closed_at)
		VALUES ($1, 'menu', 'closed', now(), now())`, uuid.New())
	if err == nil {
		t.Fatal("lifecycle check accepted a closed row without outcome")
	}
	// Two active escalations for one member violate the partial unique index.
	m := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO safety.escalations (member_id, source, opened_at) VALUES ($1, 'menu', now())`, m); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO safety.escalations (member_id, source, opened_at) VALUES ($1, 'sos', now())`, m); err == nil {
		t.Fatal("second active escalation accepted")
	}
}
