package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/service"
)

// A deleted member's tombstone refuses her identity and never makes way for
// a new member: lazy creation would otherwise give a replayed old session a
// fresh account.
func TestDeletedMemberIdentityIsRefused(t *testing.T) {
	e := newEnv(t)
	ctx, m := newMember(t, e)

	e.store.mu.Lock()
	rec := e.store.st.members[m.ID]
	rec.Deleted, rec.Roles, rec.SealedName = true, nil, nil
	e.store.st.members[m.ID] = rec
	e.store.mu.Unlock()

	for range 2 {
		_, err := e.svc.Me(ctx)
		wantErr(t, err, model.ErrAccountDeleted)
	}
	if n := e.store.memberCount(); n != 1 {
		t.Fatalf("members = %d, want 1 (no new member for a deleted identity)", n)
	}
}

type fakeEraser struct {
	calls []uuid.UUID
	err   error
}

func (f *fakeEraser) EraseMember(_ context.Context, memberID uuid.UUID, _ time.Time) error {
	f.calls = append(f.calls, memberID)
	return f.err
}

type fakeIdentities struct {
	calls []uuid.UUID
	err   error
}

func (f *fakeIdentities) DeleteIdentity(_ context.Context, id uuid.UUID) error {
	f.calls = append(f.calls, id)
	return f.err
}

func TestDeletionDeleteMember(t *testing.T) {
	clk := &fakeClock{}
	eraser := &fakeEraser{}
	d := &service.Deletion{Store: eraser, Clock: clk}
	if err := d.DeleteMember(context.Background(), uuid.Nil); err == nil {
		t.Fatal("nil member id accepted")
	}
	id := uuid.New()
	if err := d.DeleteMember(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(eraser.calls) != 1 || eraser.calls[0] != id {
		t.Fatalf("erase calls = %v", eraser.calls)
	}
	eraser.err = errors.New("database down")
	if err := d.DeleteMember(context.Background(), id); err == nil {
		t.Fatal("store failure not returned (the consumer must retry)")
	}
}

func TestDeletionDeleteIdentity(t *testing.T) {
	ids := &fakeIdentities{}
	d := &service.Deletion{Store: &fakeEraser{}, Identities: ids, Clock: &fakeClock{}}
	identity := uuid.New()
	if err := d.DeleteIdentity(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	ids.err = errors.New("kratos admin: status 503")
	if err := d.DeleteIdentity(context.Background(), identity); err == nil {
		t.Fatal("identity provider failure not returned (the timer must retry)")
	}
	if err := (&service.Deletion{Store: &fakeEraser{}, Clock: &fakeClock{}}).DeleteIdentity(context.Background(), identity); err == nil {
		t.Fatal("missing identity provider accepted")
	}
}
