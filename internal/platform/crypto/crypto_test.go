package crypto_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/mac"
	tinkpb "github.com/tink-crypto/tink-go/v2/proto/tink_go_proto"

	"birge/internal/platform/crypto"
)

// keysetJSON generates a fresh cleartext keyset; tests never hard-code keys.
func keysetJSON(t *testing.T, tmpl *tinkpb.KeyTemplate) []byte {
	t.Helper()
	h, err := keyset.NewHandle(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := insecurecleartextkeyset.Write(h, keyset.NewJSONWriter(&buf)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newKEK(t *testing.T, id string) *crypto.LocalKEK {
	t.Helper()
	k, err := crypto.NewLocalKEK(id, keysetJSON(t, aead.AES256GCMKeyTemplate()))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestLocalKEKWrapUnwrap(t *testing.T) {
	ctx := context.Background()
	k1, k2 := newKEK(t, "local-1"), newKEK(t, "local-2")
	dek := []byte("not a real key, just bytes")
	ad := []byte("platform.member_keys.wrapped_dek|a")
	w, err := k1.Wrap(ctx, dek, ad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(w, dek) {
		t.Fatal("wrapped key contains the plaintext")
	}
	got, err := k1.Unwrap(ctx, w, ad)
	if err != nil || !bytes.Equal(got, dek) {
		t.Fatalf("Unwrap = %q, %v", got, err)
	}
	if _, err := k1.Wrap(ctx, dek, nil); err == nil {
		t.Fatal("Wrap without associated data")
	}
	if _, err := k1.Unwrap(ctx, w, []byte("platform.member_keys.wrapped_dek|b")); !errors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("Unwrap with other associated data: %v", err)
	}
	if _, err := k2.Unwrap(ctx, w, ad); !errors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("another KEK unwrapped the key: %v", err)
	}
	w[len(w)-1] ^= 1
	if _, err := k1.Unwrap(ctx, w, ad); !errors.Is(err, crypto.ErrInvalidCiphertext) {
		t.Fatalf("tampered wrapped key: %v", err)
	}
}

func TestNewLocalKEKRejects(t *testing.T) {
	good := keysetJSON(t, aead.AES256GCMKeyTemplate())
	tests := []struct {
		name, id string
		keyset   []byte
	}{
		{"empty id", "", good},
		{"bad id", "Local KEK", good},
		{"not json", "local-1", []byte("{")},
		{"128-bit key", "local-1", keysetJSON(t, aead.AES128GCMKeyTemplate())},
		{"other AEAD", "local-1", keysetJSON(t, aead.ChaCha20Poly1305KeyTemplate())},
		{"not AEAD", "local-1", keysetJSON(t, mac.HMACSHA256Tag256KeyTemplate())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := crypto.NewLocalKEK(tt.id, tt.keyset); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestNewKeyring(t *testing.T) {
	k1, k2 := newKEK(t, "local-1"), newKEK(t, "local-2")
	if _, err := crypto.NewKeyring(nil); err == nil {
		t.Fatal("nil primary accepted")
	}
	if _, err := crypto.NewKeyring(k1, newKEK(t, "local-1")); err == nil {
		t.Fatal("duplicate id accepted")
	}
	r, err := crypto.NewKeyring(k2, k1)
	if err != nil {
		t.Fatal(err)
	}
	if r.Primary().ID() != "local-2" {
		t.Fatalf("primary = %s", r.Primary().ID())
	}
	if k, ok := r.Get("local-1"); !ok || k.ID() != "local-1" {
		t.Fatal("retired KEK not found")
	}
	if _, ok := r.Get("local-3"); ok {
		t.Fatal("unknown KEK found")
	}
}

func TestBlindIndex(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	b, err := crypto.NewBlindIndexer(key)
	if err != nil {
		t.Fatal(err)
	}
	const phone = "+70000000000"
	idx := b.BlindIndex("membership.phone", phone)
	if len(idx) != 64 || strings.Trim(idx, "0123456789abcdef") != "" {
		t.Fatalf("index %q is not 64 hex chars", idx)
	}
	if b.BlindIndex("membership.phone", phone) != idx {
		t.Fatal("index is not deterministic")
	}
	if b.BlindIndex("membership.contact_phone", phone) == idx {
		t.Fatal("kinds are not separated")
	}
	if b.BlindIndex("membership.phone", "+70000000001") == idx {
		t.Fatal("different values share an index")
	}
	if b.BlindIndex("membership.phone|", "+70000000000") == b.BlindIndex("membership.phone", "|+70000000000") {
		t.Fatal("kind and value are ambiguous")
	}
	other := bytes.Clone(key)
	other[0] ^= 1
	b2, err := crypto.NewBlindIndexer(other)
	if err != nil {
		t.Fatal(err)
	}
	if b2.BlindIndex("membership.phone", phone) == idx {
		t.Fatal("index does not depend on the key")
	}
	key[1] ^= 1 // the indexer keeps its own copy
	if b.BlindIndex("membership.phone", phone) != idx {
		t.Fatal("indexer aliases the caller's key")
	}
	if _, err := crypto.NewBlindIndexer(make([]byte, 31)); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestAssociatedData(t *testing.T) {
	id := uuid.MustParse("00000000-0000-7000-8000-000000000001")
	got := string(crypto.AssociatedData("membership.members.display_name", id))
	if got != "membership.members.display_name|00000000-0000-7000-8000-000000000001" {
		t.Fatalf("AssociatedData = %q", got)
	}
}
