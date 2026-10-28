package adapter_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"birge/internal/domains/emergency/adapter"
	"birge/internal/domains/emergency/model"
	"birge/internal/platform/timers"
)

// TestExternalPlanLinksPostgres runs the link lifecycle on PostgreSQL: only
// a 32-byte keyed hash is stored, the token is nowhere in the database,
// views are audited, a resend revokes, the purge timer deletes the row after
// the retention period, and account deletion removes links and timers.
func TestExternalPlanLinksPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.svc.Settings.PlanLinkKey = []byte("test-plan-link-key-0123456789abcdef")
	owner := e.member(false, nil)
	if err := e.svc.SetChildCard(ctx, owner, fakeCard); err != nil {
		t.Fatal(err)
	}
	ext, err := e.svc.AddTrustedContact(ctx, owner, key(), model.NewTrustedContact{Kind: model.ContactExternal, Name: fakeName, Phone: fakePhone})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SendPlan(ctx, owner, key(), "SEND"); err != nil {
		t.Fatal(err)
	}
	tok, err := e.svc.ExternalPlanLink(ctx, ext.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.plan_links WHERE octet_length(token_hash) = 32 AND revoked_at IS NULL AND owner_id = $1`, owner.MemberID); got != 1 {
		t.Fatalf("live links = %d", got)
	}
	// The token in clear is in no table: not the link, not the outbox, not the timers.
	for _, q := range []string{
		`SELECT count(*) FROM emergency.plan_links WHERE position(convert_to($1, 'UTF8') IN token_hash) > 0`,
		`SELECT count(*) FROM platform.outbox WHERE payload::text LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM platform.timers WHERE payload::text LIKE '%' || $1 || '%'`,
		`SELECT count(*) FROM platform.audit_log WHERE coalesce(reason, '') LIKE '%' || $1 || '%'`,
	} {
		if got := count(t, e.pool, q, tok); got != 0 {
			t.Fatalf("token in clear: %s", q)
		}
	}
	sp, err := e.svc.ViewExternalPlan(ctx, tok)
	if err != nil || sp.ChildCard != fakeCard {
		t.Fatalf("view = %+v %v", sp, err)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.audit_log WHERE action = $1 AND actor_id = $2 AND actor_role = $3 AND object_id = $4`,
		model.AuditViewExternalPlan, ext.ID, model.AuditActorExternalContact, owner.MemberID); got != 1 {
		t.Fatalf("view audit rows = %d", got)
	}

	// Resend revokes; the next SMS mints a fresh link.
	if err := e.svc.SendPlan(ctx, owner, key(), "SEND"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ViewExternalPlan(ctx, tok); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("token after resend: %v", err)
	}
	if _, err := e.svc.ExternalPlanLink(ctx, ext.ID); err != nil {
		t.Fatal(err)
	}

	// Retention: both purge timers fire 24 h + 7 days after their send.
	worker := &timers.Worker{
		Pool: e.pool, Clock: e.clock, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Interval: time.Second,
		Handlers: (&adapter.TimerHandlers{Service: e.svc}).Handlers(),
	}
	e.clock.Advance(model.PlanLinkTTL + model.PlanLinkRetention - time.Minute)
	if fired, err := worker.RunOnce(ctx); fired || err != nil {
		t.Fatalf("early purge: %v %v", fired, err)
	}
	e.clock.Advance(2 * time.Minute)
	for range 2 {
		if fired, err := worker.RunOnce(ctx); !fired || err != nil {
			t.Fatalf("purge: %v %v", fired, err)
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.plan_links`); got != 0 {
		t.Fatalf("links after retention = %d", got)
	}

	// Account deletion: a live link and its pending purge timer go.
	if err := e.svc.SendPlan(ctx, owner, key(), "SEND"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ExternalPlanLink(ctx, ext.ID); err != nil {
		t.Fatal(err)
	}
	store := &adapter.PostgresStore{Pool: e.pool}
	for range 2 {
		if err := store.EraseMember(ctx, owner.MemberID, e.clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := count(t, e.pool, `SELECT count(*) FROM emergency.plan_links`); got != 0 {
		t.Fatalf("links after deletion = %d", got)
	}
	if got := count(t, e.pool, `SELECT count(*) FROM platform.timers WHERE kind = $1 AND done_at IS NULL`, model.TimerPlanLinkPurge); got != 0 {
		t.Fatalf("pending purge timers after deletion = %d", got)
	}
}
