package adapter_test

import (
	"bytes"
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
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"

	"birge/internal/domains/emergency/adapter"
	"birge/internal/domains/emergency/emergencytest"
	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	"birge/internal/domains/emergency/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/crypto"
	"birge/internal/platform/testinfra"
	"birge/internal/platform/timers"
)

// Clearly fake personal data.
const (
	fakeText  = "example sos text need medicine"
	fakeCard  = "example child card text"
	fakeName  = "Example Contact"
	fakePhone = "+70000000001"
)

type env struct {
	pool    *pgxpool.Pool
	svc     *service.Service
	members *emergencytest.FakeMembers
	clock   *emergencytest.Clock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	members := emergencytest.NewFakeMembers()
	clk := &emergencytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	return &env{pool: pool, members: members, clock: clk, svc: &service.Service{
		Store:         &adapter.PostgresStore{Pool: pool},
		Sealer:        &emergencytest.FakeSealer{},
		Members:       members,
		Blocks:        members,
		Acquaintances: members,
		Clock:         clk,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

// realSealer uses the platform envelope with a throwaway local KEK, so tests
// can check that no plaintext reaches the database.
func realSealer(t *testing.T, pool *pgxpool.Pool) *crypto.Envelope {
	t.Helper()
	h, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := insecurecleartextkeyset.Write(h, keyset.NewJSONWriter(&buf)); err != nil {
		t.Fatal(err)
	}
	kek, err := crypto.NewLocalKEK("test", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	ring, err := crypto.NewKeyring(kek)
	if err != nil {
		t.Fatal(err)
	}
	e, err := crypto.NewEnvelope(pool, ring, crypto.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) member(verified bool, district *uuid.UUID) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{Verified: verified, DistrictID: district, DisplayName: "Example", Lang: "ru"})
	return p
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func key() uuid.UUID { return uuid.Must(uuid.NewV7()) }

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

func (t *failingTx) ScheduleAutoClose(ctx context.Context, id uuid.UUID, due time.Time) (bool, error) {
	if t.method == "ScheduleAutoClose" {
		return false, errInjected
	}
	return t.Tx.ScheduleAutoClose(ctx, id, due)
}

func (t *failingTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	if t.method == "Publish:"+e.Type {
		return errInjected
	}
	return t.Tx.Publish(ctx, now, e)
}

func TestRaiseSosAtomicPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Sealer = realSealer(t, e.pool)
	owner := e.member(false, nil)
	friend := e.member(true, nil)
	invited := e.member(true, nil)
	e.members.Meet(owner.MemberID, friend.MemberID)
	e.members.Meet(owner.MemberID, invited.MemberID)
	ext, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactExternal, Name: fakeName, Phone: fakePhone})
	if err != nil {
		t.Fatal(err)
	}
	fc, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: friend.MemberID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AcceptTrustedInvitation(ctx, friend, fc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: invited.MemberID}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetChildCard(ctx, owner, fakeCard); err != nil {
		t.Fatal(err)
	}
	outboxBefore := count(t, e.pool, `SELECT count(*) FROM platform.outbox`)

	// Injected failures leave nothing behind.
	for _, method := range []string{"ScheduleAutoClose", "Publish:" + model.EventDeliveryRequested} {
		svc := *e.svc
		svc.Store = &failingStore{inner: &adapter.PostgresStore{Pool: e.pool}, method: method}
		if _, err := svc.RaiseSos(ctx, owner, key(), model.SosChildcare, fakeText); !errors.Is(err, errInjected) {
			t.Fatalf("%s: err = %v", method, err)
		}
	}
	if count(t, e.pool, `SELECT count(*) FROM emergency.sos_requests`) != 0 ||
		count(t, e.pool, `SELECT count(*) FROM platform.outbox`) != outboxBefore ||
		count(t, e.pool, `SELECT count(*) FROM platform.timers`) != 0 {
		t.Fatal("a failed raise committed something")
	}

	v, err := e.svc.RaiseSos(ctx, owner, key(), model.SosChildcare, fakeText)
	if err != nil || v.PlainText != fakeText {
		t.Fatalf("raise: %+v %v", v, err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventSosRaised); got != 1 {
		t.Fatalf("raised events = %d", got)
	}
	rows, err := e.pool.Query(ctx, `SELECT payload FROM platform.outbox WHERE event_type = $1 AND payload->'params'->>'sos_id' = $2`,
		model.EventDeliveryRequested, v.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var d struct {
			RequestID uuid.UUID         `json:"request_id"`
			Recipient map[string]string `json:"recipient"`
			Class     string            `json:"class"`
			Template  string            `json:"template"`
		}
		if err := json.Unmarshal(payload, &d); err != nil {
			t.Fatal(err)
		}
		if len(d.Recipient) != 1 || d.Class != model.ClassCritical || d.RequestID == uuid.Nil {
			t.Fatalf("delivery = %s", payload)
		}
		for k, val := range d.Recipient {
			got[d.Template+"|"+k+"|"+val]++
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		model.TemplateSosRaised + "|staff_role|moderator":                         1,
		model.TemplateSosRaisedContact + "|member_id|" + friend.MemberID.String(): 1,
		model.TemplateSosRaisedContact + "|trusted_contact_id|" + ext.ID.String(): 1,
	}
	if len(got) != len(want) {
		t.Fatalf("deliveries = %v", got)
	}
	for k, n := range want {
		if got[k] != n {
			t.Fatalf("deliveries = %v, want %v", got, want)
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.timers WHERE kind = $1 AND subject_id = $2 AND due_at = $3 AND done_at IS NULL`,
		model.TimerSosAutoClose, v.ID, e.clock.Now().Add(24*time.Hour)); got != 1 {
		t.Fatalf("timers = %d", got)
	}
	// No plaintext anywhere: not in the outbox, not in emergency's tables.
	for _, secret := range []string{fakeText, fakeCard, fakeName, fakePhone} {
		if n := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE position($1 in payload::text) > 0`, secret); n != 0 {
			t.Fatalf("outbox contains %q", secret)
		}
		if n := count(t, e.pool, `SELECT
			(SELECT count(*) FROM emergency.sos_requests WHERE position(convert_to($1, 'UTF8') in content) > 0) +
			(SELECT count(*) FROM emergency.child_cards WHERE position(convert_to($1, 'UTF8') in content) > 0) +
			(SELECT count(*) FROM emergency.trusted_contacts WHERE position(convert_to($1, 'UTF8') in coalesce(name, '')) > 0
			                                                  OR position(convert_to($1, 'UTF8') in coalesce(phone, '')) > 0)`, secret); n != 0 {
			t.Fatalf("emergency tables contain %q in clear", secret)
		}
	}
	// The sealed values open again for display and for notify.
	phone, lang, err := e.svc.ContactPhone(ctx, ext.ID)
	if err != nil || phone != fakePhone || lang != "ru" {
		t.Fatalf("contact phone: %q %q %v", phone, lang, err)
	}
	plan, err := e.svc.GetPlan(ctx, owner)
	if err != nil || plan.ChildCard != fakeCard || len(plan.Contacts) != 3 {
		t.Fatalf("plan: %+v %v", plan, err)
	}
}

func TestRateLimitUnderConcurrencyPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := e.member(false, nil)
	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			_, errs[i] = e.svc.RaiseSos(ctx, p, key(), model.SosOther, fakeText)
		})
	}
	close(start)
	wg.Wait()
	ok, limited := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, model.ErrRateLimited):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if ok != model.MaxSosPerHour || limited != n-model.MaxSosPerHour {
		t.Fatalf("ok=%d limited=%d", ok, limited)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.sos_requests WHERE member_id = $1`, p.MemberID); got != model.MaxSosPerHour {
		t.Fatalf("rows = %d", got)
	}
	// The same key from concurrent retries gives one SOS.
	q := e.member(false, nil)
	k := key()
	ids := make([]uuid.UUID, n)
	for i := range n {
		wg.Go(func() {
			v, err := e.svc.RaiseSos(ctx, q, k, model.SosOther, fakeText)
			if err != nil {
				t.Error(err)
			}
			ids[i] = v.ID
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatal("replays returned different ids")
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->>'member_id' = $2`,
		model.EventSosRaised, q.MemberID.String()); got != 1 {
		t.Fatalf("raised events for one key = %d", got)
	}
}

func TestFeedChildcareAndBlocksPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	d := uuid.New()
	author := e.member(true, &d)
	viewer := e.member(true, &d)
	blocked := e.member(true, &d)

	child, err := e.svc.RaiseSos(ctx, author, key(), model.SosChildcare, fakeText)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ShareSos(ctx, author, child.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("share childcare: %v", err)
	}
	// The table refuses it even when written directly.
	if _, err := e.pool.Exec(ctx, `UPDATE emergency.sos_requests SET district_id = $1, shared_at = now() WHERE id = $2`, d, child.ID); err == nil {
		t.Fatal("the database accepted a shared childcare SOS")
	}
	// The store's share update refuses it too.
	if err := (&adapter.PostgresStore{Pool: e.pool}).InTx(ctx, func(tx port.Tx) error {
		if _, ok, err := tx.ShareSos(ctx, child.ID, d, e.clock.Now()); err != nil || ok {
			t.Fatalf("store shared childcare: %v %v", ok, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	share := func(p auth.Principal) model.SosView {
		v, err := e.svc.RaiseSos(ctx, p, key(), model.SosItemOrMedicine, fakeText)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.ShareSos(ctx, p, v.ID); err != nil {
			t.Fatal(err)
		}
		return v
	}
	visible := share(author)
	share(viewer)
	share(blocked)
	closed := share(author)
	if _, err := e.svc.CloseSos(ctx, author, closed.ID); err != nil {
		t.Fatal(err)
	}
	e.members.Block(viewer.MemberID, blocked.MemberID)

	page, err := e.svc.Feed(ctx, viewer, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != visible.ID {
		t.Fatalf("feed = %+v", page.Items)
	}
	// The SQL filter itself never returns childcare, own or closed rows.
	if err := (&adapter.PostgresStore{Pool: e.pool}).InTx(ctx, func(tx port.Tx) error {
		rows, err := tx.ListSosFeed(ctx, model.FeedFilter{ViewerID: viewer.MemberID, DistrictID: d, Now: e.clock.Now(), Limit: 50})
		if err != nil {
			return err
		}
		if len(rows) != 2 { // author's open one and the blocked member's (blocks are applied by the service)
			t.Fatalf("feed rows = %d", len(rows))
		}
		for _, r := range rows {
			if r.Type == model.SosChildcare || r.AuthorID == viewer.MemberID || r.ID == closed.ID {
				t.Fatalf("feed row must not be returned: %+v", r)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Feed(ctx, e.member(false, &d), ""); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unverified: %v", err)
	}

	// Respond: idempotent, one row and one notification.
	for range 3 {
		if err := e.svc.RespondToSos(ctx, viewer, visible.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.sos_responses WHERE sos_id = $1`, visible.ID); got != 1 {
		t.Fatalf("responses = %d", got)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->>'template' = $2 AND payload->'recipient'->>'member_id' = $3 AND payload->>'class' = 'normal'`,
		model.EventDeliveryRequested, model.TemplateSosResponded, author.MemberID.String()); got != 1 {
		t.Fatalf("author notifications = %d", got)
	}
	if err := e.svc.RespondToSos(ctx, blocked, visible.ID); err != nil {
		t.Fatalf("not blocked by author: %v", err)
	}
	e.members.Meet(author.MemberID, viewer.MemberID)
	list, err := e.svc.ListResponses(ctx, author, visible.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("responses: %+v %v", list, err)
	}
	var viewerResp uuid.UUID
	for _, r := range list {
		if r.ResponderID == viewer.MemberID {
			if !r.MetInPerson {
				t.Fatal("metInPerson false")
			}
			viewerResp = r.ID
		}
	}
	for range 2 {
		if err := e.svc.AcceptResponse(ctx, author, visible.ID, viewerResp); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->>'responder_id' = $2`,
		model.EventSosResponseAccepted, viewer.MemberID.String()); got != 1 {
		t.Fatalf("accepted events = %d", got)
	}
	if mine, err := e.svc.ListMySos(ctx, author); err != nil || len(mine) != 3 {
		t.Fatalf("my sos: %d %v", len(mine), err)
	}
}

func TestAutoCloseTimerPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	worker := &timers.Worker{
		Pool: e.pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Interval: time.Second,
		Handlers: (&adapter.TimerHandlers{Service: e.svc}).Handlers(),
	}
	p := e.member(false, nil)
	byAuthor, err := e.svc.RaiseSos(ctx, p, key(), model.SosOther, fakeText)
	if err != nil {
		t.Fatal(err)
	}
	auto, err := e.svc.RaiseSos(ctx, p, key(), model.SosOther, fakeText)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CloseSos(ctx, p, byAuthor.ID); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.timers WHERE subject_id = $1 AND done_at IS NOT NULL AND last_error = 'cancelled'`, byAuthor.ID); got != 1 {
		t.Fatalf("author-closed timer not cancelled: %d", got)
	}
	if fired, err := worker.RunOnce(ctx); fired || err != nil {
		t.Fatalf("early: %v %v", fired, err)
	}
	e.clock.Advance(24*time.Hour + time.Second)
	if fired, err := worker.RunOnce(ctx); !fired || err != nil {
		t.Fatalf("due: %v %v", fired, err)
	}
	if fired, _ := worker.RunOnce(ctx); fired {
		t.Fatal("a second timer fired")
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.sos_requests WHERE id = $1 AND status = 'closed' AND close_reason = 'auto'`, auto.ID); got != 1 {
		t.Fatal("not auto-closed")
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1`, model.EventSosClosed); got != 2 {
		t.Fatalf("closed events = %d", got)
	}
}

func TestTrustedContactsAndSharedPlanPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := e.member(false, nil)
	friend := e.member(true, nil)
	stranger := e.member(true, nil)

	if _, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: stranger.MemberID}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unmet member: %v", err)
	}
	e.members.Meet(owner.MemberID, friend.MemberID)
	inv, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: friend.MemberID})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AcceptTrustedInvitation(ctx, stranger, inv.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("wrong member accepts: %v", err)
	}
	if err := e.svc.AcceptTrustedInvitation(ctx, friend, inv.ID); err != nil {
		t.Fatal(err)
	}
	// Five concurrent adds with two slots left: exactly two succeed.
	const n = 5
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			_, errs[i] = e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactExternal, Name: fakeName, Phone: fakePhone})
		})
	}
	wg.Wait()
	added := 0
	for _, err := range errs {
		switch {
		case err == nil:
			added++
		case !errors.Is(err, model.ErrConflict):
			t.Fatal(err)
		}
	}
	if added != 2 || count(t, e.pool, `SELECT count(*) FROM emergency.trusted_contacts WHERE owner_id = $1 AND removed_at IS NULL`, owner.MemberID) != 3 {
		t.Fatalf("added = %d", added)
	}

	if err := e.svc.SetChildCard(ctx, owner, fakeCard); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("before send: %v", err)
	}
	if err := e.svc.SendPlan(ctx, owner, key(), "SEND"); err != nil {
		t.Fatal(err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->>'template' = $2`,
		model.EventDeliveryRequested, model.TemplatePlanSent); got != 3 {
		t.Fatalf("plan deliveries = %d", got)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.outbox WHERE event_type = $1 AND payload->>'member_id' = $2`,
		model.EventPlanSent, owner.MemberID.String()); got != 1 {
		t.Fatalf("plan sent events = %d", got)
	}
	if _, err := e.svc.GetSharedPlan(ctx, stranger, owner.MemberID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("stranger: %v", err)
	}
	sp, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID)
	if err != nil || sp.ChildCard != fakeCard {
		t.Fatalf("shared plan: %+v %v", sp, err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log WHERE actor_id = $1 AND actor_role = 'member' AND action = $2 AND object_type = $3 AND object_id = $4`,
		friend.MemberID, model.AuditViewSharedPlan, model.AuditObjectChildCard, owner.MemberID); got != 1 {
		t.Fatalf("audit rows = %d", got)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log`); got != 1 {
		t.Fatalf("denied reads audited: %d", got)
	}
	// Removal keeps the row and ends access and notify resolution.
	if err := e.svc.RemoveTrustedContact(ctx, owner, inv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.GetSharedPlan(ctx, friend, owner.MemberID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("after removal: %v", err)
	}
	if _, _, err := e.svc.TrustedContactTarget(ctx, inv.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("target after removal: %v", err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.trusted_contacts WHERE id = $1 AND removed_at IS NOT NULL`, inv.ID); got != 1 {
		t.Fatal("removed row not kept")
	}
	// The removed member can be invited again.
	if _, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactMember, MemberID: friend.MemberID}); err != nil {
		t.Fatalf("re-invite: %v", err)
	}
}

func TestSchemaConstraintsPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner := uuid.New()
	// An external contact without a phone violates the kind check.
	if _, err := e.pool.Exec(ctx, `INSERT INTO emergency.trusted_contacts (id, owner_id, idempotency_key, kind, name, lang, status, created_at, confirmed_at)
		VALUES ($1, $2, $3, 'external', '\x01', 'ru', 'confirmed', now(), now())`, uuid.New(), owner, uuid.New()); err == nil {
		t.Fatal("external contact without phone accepted")
	}
	// A member cannot be her own contact.
	if _, err := e.pool.Exec(ctx, `INSERT INTO emergency.trusted_contacts (id, owner_id, idempotency_key, kind, contact_member_id, lang, status, created_at)
		VALUES ($1, $2, $3, 'member', $2, 'ru', 'invited', now())`, uuid.New(), owner, uuid.New()); err == nil {
		t.Fatal("self contact accepted")
	}
	// A closed SOS needs a reason.
	if _, err := e.pool.Exec(ctx, `INSERT INTO emergency.sos_requests (id, member_id, idempotency_key, type, content, status, created_at, closes_at, closed_at)
		VALUES ($1, $2, $3, 'other', '\x01', 'closed', now(), now() + interval '1 hour', now())`, uuid.New(), owner, uuid.New()); err == nil {
		t.Fatal("closed SOS without reason accepted")
	}
}
