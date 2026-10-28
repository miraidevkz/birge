package crypto_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/crypto"
	"birge/internal/platform/testinfra"
)

const field = "test.records.note"

func newEnvelope(t *testing.T, pool *pgxpool.Pool, primary crypto.KEK, previous ...crypto.KEK) *crypto.Envelope {
	t.Helper()
	ring, err := crypto.NewKeyring(primary, previous...)
	if err != nil {
		t.Fatal(err)
	}
	e, err := crypto.NewEnvelope(pool, ring, crypto.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func seal(ctx context.Context, t *testing.T, s crypto.Sealer, member uuid.UUID, pt, ad []byte) []byte {
	t.Helper()
	ct, err := s.Seal(ctx, member, pt, ad)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}

func mustOpen(ctx context.Context, t *testing.T, s crypto.Sealer, member uuid.UUID, ct, ad, want []byte) {
	t.Helper()
	got, err := s.Open(ctx, member, ct, ad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("Open returned different plaintext")
	}
}

func TestEnvelopeSealOpen(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	kek := newKEK(t, "local-1")
	e := newEnvelope(t, pool, kek)
	alice, bob := uuid.New(), uuid.New()
	ad := crypto.AssociatedData(field, alice)
	pt := []byte("synthetic note")

	ct := seal(ctx, t, e, alice, pt, ad)
	if bytes.Contains(ct, pt) {
		t.Fatal("ciphertext contains the plaintext")
	}
	mustOpen(ctx, t, e, alice, ct, ad, pt)
	// A fresh process (empty cache) opens it from the stored key.
	mustOpen(ctx, t, newEnvelope(t, pool, kek), alice, ct, ad, pt)
	// Empty plaintext is a valid value.
	mustOpen(ctx, t, e, alice, seal(ctx, t, e, alice, nil, ad), ad, nil)

	t.Run("wrong associated data", func(t *testing.T) {
		for _, wrong := range [][]byte{
			crypto.AssociatedData("test.records.other", alice),
			crypto.AssociatedData(field, uuid.New()),
		} {
			if _, err := e.Open(ctx, alice, ct, wrong); !errors.Is(err, crypto.ErrInvalidCiphertext) {
				t.Fatalf("Open with wrong AD: %v", err)
			}
		}
	})
	t.Run("another member", func(t *testing.T) {
		seal(ctx, t, e, bob, []byte("other"), ad) // bob has a key of his own
		if _, err := e.Open(ctx, bob, ct, ad); !errors.Is(err, crypto.ErrInvalidCiphertext) {
			t.Fatalf("bob opened alice's data: %v", err)
		}
		if _, err := e.Open(ctx, uuid.New(), ct, ad); !errors.Is(err, crypto.ErrInvalidCiphertext) {
			t.Fatalf("member without a key: %v", err)
		}
	})
	t.Run("tampered", func(t *testing.T) {
		bad := bytes.Clone(ct)
		bad[len(bad)-1] ^= 1
		if _, err := e.Open(ctx, alice, bad, ad); !errors.Is(err, crypto.ErrInvalidCiphertext) {
			t.Fatalf("tampered ciphertext: %v", err)
		}
	})
	t.Run("arguments", func(t *testing.T) {
		if _, err := e.Seal(ctx, alice, pt, nil); err == nil {
			t.Fatal("Seal without associated data")
		}
		if _, err := e.Seal(ctx, uuid.Nil, pt, ad); err == nil {
			t.Fatal("Seal without member")
		}
		if _, err := e.Open(ctx, alice, ct, nil); err == nil {
			t.Fatal("Open without associated data")
		}
	})
	t.Run("unknown KEK", func(t *testing.T) {
		other := newEnvelope(t, pool, newKEK(t, "local-9"))
		if _, err := other.Open(ctx, alice, ct, ad); err == nil || errors.Is(err, crypto.ErrInvalidCiphertext) {
			t.Fatalf("Open without the wrapping KEK: %v", err)
		}
	})
}

func TestEnvelopeShred(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	kek := newKEK(t, "local-1")
	e := newEnvelope(t, pool, kek)
	other := newEnvelope(t, pool, kek) // a second replica with a cold cache
	member, bystander := uuid.New(), uuid.New()
	ad := crypto.AssociatedData(field, member)
	ct := seal(ctx, t, e, member, []byte("synthetic note"), ad)
	keep := seal(ctx, t, e, bystander, []byte("kept"), ad)

	for range 2 { // idempotent
		if err := e.Shred(ctx, member); err != nil {
			t.Fatal(err)
		}
	}
	for name, s := range map[string]*crypto.Envelope{"same": e, "other": other} {
		if _, err := s.Open(ctx, member, ct, ad); !errors.Is(err, crypto.ErrShredded) {
			t.Fatalf("%s: Open after Shred: %v", name, err)
		}
		if _, err := s.Seal(ctx, member, []byte("new"), ad); !errors.Is(err, crypto.ErrShredded) {
			t.Fatalf("%s: Seal after Shred: %v", name, err)
		}
	}
	mustOpen(ctx, t, e, bystander, keep, ad, []byte("kept"))

	var wrapped []byte
	var kekID string
	var destroyed bool
	if err := pool.QueryRow(ctx,
		`SELECT wrapped_dek, kek_id, destroyed_at IS NOT NULL FROM platform.member_keys WHERE member_id = $1`, member,
	).Scan(&wrapped, &kekID, &destroyed); err != nil {
		t.Fatal(err)
	}
	if len(wrapped) != 0 || !destroyed || kekID != "local-1" {
		t.Fatalf("tombstone: %d bytes, destroyed=%v, kek=%s", len(wrapped), destroyed, kekID)
	}

	// Shredding a member who never sealed anything leaves a tombstone too.
	never := uuid.New()
	if err := e.Shred(ctx, never); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Seal(ctx, never, []byte("new"), ad); !errors.Is(err, crypto.ErrShredded) {
		t.Fatalf("Seal for a pre-shredded member: %v", err)
	}
}

func TestEnvelopeWrappedKeyBoundToMember(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	kek := newKEK(t, "local-1")
	a, b := uuid.New(), uuid.New()
	adA, adB := crypto.AssociatedData(field, a), crypto.AssociatedData(field, b)
	writer := newEnvelope(t, pool, kek)
	ctA := seal(ctx, t, writer, a, []byte("a's note"), adA)
	ctB := seal(ctx, t, writer, b, []byte("b's note"), adB)

	// An attacker with write access to the table copies B's wrapped key into A's row.
	if _, err := pool.Exec(ctx,
		`UPDATE platform.member_keys SET wrapped_dek = (SELECT wrapped_dek FROM platform.member_keys WHERE member_id = $2) WHERE member_id = $1`,
		a, b,
	); err != nil {
		t.Fatal(err)
	}
	e := newEnvelope(t, pool, kek) // cold cache: reads the tampered row
	if _, err := e.Seal(ctx, a, []byte("new"), adA); !errors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("Seal with a copied key: %v", err)
	}
	if _, err := e.Open(ctx, a, ctA, adA); !errors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("Open with a copied key: %v", err)
	}
	mustOpen(ctx, t, e, b, ctB, adB, []byte("b's note"))
	mustOpen(ctx, t, e, b, seal(ctx, t, e, b, []byte("more"), adB), adB, []byte("more"))
}

// TestEnvelopeKEKRotation follows the rotation procedure in
// docs/development.md: data sealed under the old KEK opens after the
// rotation, its data key moves to the new primary on first use, new data is
// sealed under the new primary, and once every live row is re-wrapped the
// old KEK can leave the keyring.
func TestEnvelopeKEKRotation(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	k1, k2 := newKEK(t, "local-1"), newKEK(t, "local-2")
	kekOf := func(id uuid.UUID) string {
		t.Helper()
		var k string
		if err := pool.QueryRow(ctx, `SELECT kek_id FROM platform.member_keys WHERE member_id = $1`, id).Scan(&k); err != nil {
			t.Fatal(err)
		}
		return k
	}
	old, idle, gone := uuid.New(), uuid.New(), uuid.New()
	ad, adIdle, adGone := crypto.AssociatedData(field, old), crypto.AssociatedData(field, idle), crypto.AssociatedData(field, gone)
	before := newEnvelope(t, pool, k1)
	ct := seal(ctx, t, before, old, []byte("before rotation"), ad)
	ctIdle := seal(ctx, t, before, idle, []byte("idle member"), adIdle)
	seal(ctx, t, before, gone, []byte("deleted member"), adGone)
	if err := before.Shred(ctx, gone); err != nil {
		t.Fatal(err)
	}

	// Step 1: every replica learns the new KEK as a previous one; nothing
	// is wrapped with it yet and nothing moves.
	step1 := newEnvelope(t, pool, k1, k2)
	mustOpen(ctx, t, step1, old, ct, ad, []byte("before rotation"))
	if got := kekOf(old); got != "local-1" {
		t.Fatalf("step 1 moved the key to %s", got)
	}

	// Step 2: the new KEK becomes primary. The old ciphertext opens, and its
	// data key is re-wrapped with the primary on the way.
	rotated := newEnvelope(t, pool, k2, k1)
	mustOpen(ctx, t, rotated, old, ct, ad, []byte("before rotation"))
	if got := kekOf(old); got != "local-2" {
		t.Fatalf("after first use the key is wrapped by %s, want local-2", got)
	}
	after := seal(ctx, t, rotated, old, []byte("after"), ad)
	fresh := uuid.New()
	adFresh := crypto.AssociatedData(field, fresh)
	ctFresh := seal(ctx, t, rotated, fresh, []byte("new member"), adFresh)
	if got := kekOf(fresh); got != "local-2" {
		t.Fatalf("new member kek = %s", got)
	}
	// A replica still on the step-1 keyring (rolling update) reads rows the
	// new primary wrapped. It re-wraps them with its own primary, local-1;
	// harmless, both keyrings open both, and the first load on an updated
	// replica moves the row back once the rollout is done.
	lagging := newEnvelope(t, pool, k1, k2)
	mustOpen(ctx, t, lagging, fresh, ctFresh, adFresh, []byte("new member"))
	if got := kekOf(fresh); got != "local-1" {
		t.Fatalf("after a lagging replica: %s, want local-1", got)
	}
	mustOpen(ctx, t, newEnvelope(t, pool, k2, k1), fresh, ctFresh, adFresh, []byte("new member"))
	if got := kekOf(fresh); got != "local-2" {
		t.Fatalf("after an updated replica: %s, want local-2", got)
	}
	// A shredded row is never re-wrapped or revived.
	if _, err := rotated.Open(ctx, gone, []byte("x"), adGone); !errors.Is(err, crypto.ErrShredded) {
		t.Fatalf("shredded member: %v", err)
	}
	if got := kekOf(gone); got != "local-1" {
		t.Fatalf("shredded row kek = %q, want it untouched (local-1)", got)
	}

	// The idle member has not been read since the rotation: still local-1,
	// so local-1 must stay in the keyring. Reading her moves her too.
	if got := kekOf(idle); got != "local-1" {
		t.Fatalf("idle member kek = %s", got)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform.member_keys WHERE kek_id = 'local-1' AND destroyed_at IS NULL`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 1 {
		t.Fatalf("%d live rows on local-1, want 1 (the idle member)", left)
	}
	mustOpen(ctx, t, newEnvelope(t, pool, k2, k1), idle, ctIdle, adIdle, []byte("idle member"))

	// Step 3: no live row names local-1; it leaves the keyring and
	// everything still opens with the new KEK alone.
	retired := newEnvelope(t, pool, k2)
	mustOpen(ctx, t, retired, old, ct, ad, []byte("before rotation"))
	mustOpen(ctx, t, retired, old, after, ad, []byte("after"))
	mustOpen(ctx, t, retired, idle, ctIdle, adIdle, []byte("idle member"))
	mustOpen(ctx, t, retired, fresh, ctFresh, adFresh, []byte("new member"))
}

func TestEnvelopeConcurrentFirstSeal(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	kek := newKEK(t, "local-1")
	replicas := make([]*crypto.Envelope, 4)
	for i := range replicas {
		replicas[i] = newEnvelope(t, pool, kek)
	}
	member := uuid.New()
	ad := crypto.AssociatedData(field, member)

	const writers = 16
	cts := make([][]byte, writers)
	errs := make([]error, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			<-start
			cts[i], errs[i] = replicas[i%len(replicas)].Seal(ctx, member, []byte{byte(i)}, ad)
		})
	}
	close(start)
	wg.Wait()

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM platform.member_keys WHERE member_id = $1`, member).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d keys for one member, want 1", n)
	}
	reader := newEnvelope(t, pool, kek)
	for i := range writers {
		if errs[i] != nil {
			t.Fatalf("writer %d: %v", i, errs[i])
		}
		mustOpen(ctx, t, reader, member, cts[i], ad, []byte{byte(i)})
	}
}
