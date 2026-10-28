package service_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/service"
	"birge/internal/platform/auth"
)

// linkEnv is newEnv with a plan link key, a recorder of store calls and a
// cleanup that fails when any minted token reached the logs or the store.
type linkEnv struct {
	*env
	mu     sync.Mutex
	calls  []string
	tokens []string
}

func newLinkEnv(t *testing.T) *linkEnv {
	t.Helper()
	e := &linkEnv{env: newEnv(t)}
	e.svc.Settings.PlanLinkKey = []byte("test-plan-link-key-0123456789abcdef")
	e.store.FailOn = func(method string) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.calls = append(e.calls, method)
		return nil
	}
	t.Cleanup(func() {
		out := e.logs.String()
		for _, tok := range e.tokens {
			if strings.Contains(out, tok) {
				t.Errorf("logs contain a plan link token")
			}
			for _, l := range e.store.PlanLinks() {
				if bytes.Contains(l.TokenHash, []byte(tok)) {
					t.Errorf("store holds a token in clear")
				}
			}
		}
	})
	return e
}

func (e *linkEnv) mint(t *testing.T, contactID uuid.UUID) string {
	t.Helper()
	tok, err := e.svc.ExternalPlanLink(context.Background(), contactID)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if len(tok) != 43 || strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token shape: %d characters", len(tok))
	}
	e.tokens = append(e.tokens, tok)
	return tok
}

// viewCalls views tok and returns the error and the store calls it made.
func (e *linkEnv) viewCalls(tok string) (model.SharedPlan, []string, error) {
	e.mu.Lock()
	e.calls = nil
	e.mu.Unlock()
	sp, err := e.svc.ViewExternalPlan(context.Background(), tok)
	e.mu.Lock()
	defer e.mu.Unlock()
	return sp, append([]string(nil), e.calls...), err
}

// planWithExternal sets up an owner with a card, an external contact and a
// confirmed member contact, and sends the plan.
func (e *linkEnv) planWithExternal(t *testing.T) (owner uuid.UUID, ext, member model.ContactView) {
	t.Helper()
	ctx := context.Background()
	o := e.member(false, nil)
	if err := e.svc.SetChildCard(ctx, o, fakeCard); err != nil {
		t.Fatal(err)
	}
	ext = e.addExternal(t, o)
	member = e.addMember(t, o, e.member(true, nil), true)
	if _, err := e.svc.ExternalPlanLink(ctx, ext.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("link before any send: %v", err)
	}
	if err := e.svc.SendPlan(ctx, o, uuid.New(), "SEND"); err != nil {
		t.Fatal(err)
	}
	return o.MemberID, ext, member
}

func TestExternalPlanLinkLifecycle(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	sentAt := e.clock.Now()
	owner, ext, member := e.planWithExternal(t)

	if _, err := e.svc.ExternalPlanLink(ctx, member.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("member contact gets no link: %v", err)
	}
	if _, err := e.svc.ExternalPlanLink(ctx, uuid.New()); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("unknown contact: %v", err)
	}

	e.clock.Advance(time.Hour) // the SMS goes out later; expiry still counts from the send
	t1 := e.mint(t, ext.ID)
	links := e.store.PlanLinks()
	if len(links) != 1 || !links[0].ExpiresAt.Equal(sentAt.Add(24*time.Hour)) || links[0].OwnerID != owner || len(links[0].TokenHash) != 32 {
		t.Fatalf("link = %+v", links)
	}
	if tm, ok := e.store.PurgeTimer(links[0].ID); !ok || !tm.DueAt.Equal(sentAt.Add(24*time.Hour+7*24*time.Hour)) {
		t.Fatalf("purge timer = %+v %v", tm, ok)
	}
	sp, _, err := e.viewCalls(t1)
	if err != nil || sp.ChildCard != fakeCard || sp.OwnerDisplayName != fakeDisplay || !sp.SentAt.Equal(sentAt) {
		t.Fatalf("view = %+v %v", sp, err)
	}
	audit := e.store.Audit()
	last := audit[len(audit)-1]
	if last.Action != model.AuditViewExternalPlan || last.ActorID != ext.ID || last.ActorRole != model.AuditActorExternalContact ||
		last.ObjectType != model.AuditObjectChildCard || last.ObjectID != owner || last.Reason != "" {
		t.Fatalf("audit = %+v", last)
	}

	// A new link for the same contact (a retried SMS) revokes the old one.
	t2 := e.mint(t, ext.ID)
	if _, _, err := e.viewCalls(t1); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("superseded token: %v", err)
	}
	if _, _, err := e.viewCalls(t2); err != nil {
		t.Fatalf("new token: %v", err)
	}

	// Sending again revokes every earlier link at once.
	e.clock.Advance(time.Hour)
	resent := e.clock.Now()
	oid := e.ownerPrincipal(owner)
	if err := e.svc.SendPlan(ctx, oid, uuid.New(), "SEND"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.viewCalls(t2); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("token after resend: %v", err)
	}
	t3 := e.mint(t, ext.ID)
	if sp, _, err := e.viewCalls(t3); err != nil || !sp.SentAt.Equal(resent) {
		t.Fatalf("token of the new send: %+v %v", sp, err)
	}

	// 24 hours after the send the link and minting are over.
	e.clock.T = resent.Add(24 * time.Hour)
	if _, _, err := e.viewCalls(t3); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("expired token: %v", err)
	}
	if _, err := e.svc.ExternalPlanLink(ctx, ext.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("mint after expiry: %v", err)
	}
}

// ownerPrincipal is a session of an existing member.
func (e *linkEnv) ownerPrincipal(id uuid.UUID) auth.Principal {
	return auth.Principal{MemberID: id, IdentityID: uuid.New()}
}

func TestExternalPlanLinkRevokedOnContactRemoval(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	owner, ext, _ := e.planWithExternal(t)
	tok := e.mint(t, ext.ID)
	if err := e.svc.RemoveTrustedContact(ctx, e.ownerPrincipal(owner), ext.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.viewCalls(tok); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("token of a removed contact: %v", err)
	}
	if _, err := e.svc.ExternalPlanLink(ctx, ext.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("mint for a removed contact: %v", err)
	}
}

// TestExternalPlanFailuresLookAlike: unknown, revoked and expired tokens all
// fail with ErrNotFound after exactly the same single store lookup, and
// none writes an audit entry.
func TestExternalPlanFailuresLookAlike(t *testing.T) {
	e := newLinkEnv(t)
	_, ext, _ := e.planWithExternal(t)
	revoked := e.mint(t, ext.ID)
	expired := e.mint(t, ext.ID) // revokes the first
	e.clock.Advance(25 * time.Hour)
	auditBefore := len(e.store.Audit())

	cases := map[string]string{
		"unknown": strings.Repeat("A", 43),
		"revoked": revoked,
		"expired": expired,
	}
	for name, tok := range cases {
		_, calls, err := e.viewCalls(tok)
		if !errors.Is(err, model.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
		if len(calls) != 1 || calls[0] != "GetLivePlanLink" {
			t.Errorf("%s: store calls %v, want exactly [GetLivePlanLink]", name, calls)
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("A", 44), strings.Repeat("+", 43)} {
		if _, _, err := e.viewCalls(bad); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("malformed %q: %v", bad, err)
		}
	}
	if n := len(e.store.Audit()); n != auditBefore {
		t.Fatalf("failed views wrote %d audit entries", n-auditBefore)
	}
}

func TestExternalPlanAuditFailureShowsNothing(t *testing.T) {
	e := newLinkEnv(t)
	_, ext, _ := e.planWithExternal(t)
	tok := e.mint(t, ext.ID)
	boom := errors.New("audit down")
	e.store.FailOn = func(method string) error {
		if method == "Audit" {
			return boom
		}
		return nil
	}
	if sp, err := e.svc.ViewExternalPlan(context.Background(), tok); !errors.Is(err, boom) || sp.ChildCard != "" {
		t.Fatalf("view with failing audit = %+v %v", sp, err)
	}
}

func TestExternalPlanLinkMintRollsBack(t *testing.T) {
	e := newLinkEnv(t)
	_, ext, _ := e.planWithExternal(t)
	old := e.mint(t, ext.ID)
	e.store.FailOn = func(method string) error {
		if method == "SchedulePlanLinkPurge" {
			return errors.New("timers down")
		}
		return nil
	}
	if _, err := e.svc.ExternalPlanLink(context.Background(), ext.ID); err == nil {
		t.Fatal("mint with failing timer succeeded")
	}
	e.store.FailOn = nil
	if len(e.store.PlanLinks()) != 1 {
		t.Fatalf("links after rollback = %d", len(e.store.PlanLinks()))
	}
	if _, err := e.svc.ViewExternalPlan(context.Background(), old); err != nil {
		t.Fatalf("old link revoked by a rolled-back mint: %v", err)
	}
}

func TestPlanLinkPurge(t *testing.T) {
	e := newLinkEnv(t)
	ctx := context.Background()
	_, ext, _ := e.planWithExternal(t)
	e.mint(t, ext.ID)
	id := e.store.PlanLinks()[0].ID

	run := func() {
		tx, commit := e.store.Tx()
		if err := e.svc.HandlePlanLinkPurge(ctx, tx, id); err != nil {
			t.Fatal(err)
		}
		commit()
	}
	e.clock.Advance(24*time.Hour + 7*24*time.Hour - time.Minute)
	run()
	if len(e.store.PlanLinks()) != 1 {
		t.Fatal("purged before the retention period")
	}
	e.clock.Advance(time.Minute)
	run()
	if len(e.store.PlanLinks()) != 0 {
		t.Fatal("not purged after the retention period")
	}
	run() // already gone: nothing to do
}

func TestPlanLinkNeedsKey(t *testing.T) {
	e := newLinkEnv(t)
	_, ext, _ := e.planWithExternal(t)
	e.svc.Settings = service.Settings{}
	if _, err := e.svc.ExternalPlanLink(context.Background(), ext.ID); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("mint without key: %v", err)
	}
	if _, err := e.svc.ViewExternalPlan(context.Background(), strings.Repeat("A", 43)); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("view without key: %v", err)
	}
}
