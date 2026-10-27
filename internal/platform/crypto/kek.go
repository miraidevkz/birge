package crypto

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/aead/aesgcm"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/tink"
)

// KEK wraps and unwraps data keys. ID names the key in platform.member_keys.kek_id
// and must never be reused for different key material. associatedData binds the
// wrapped key to its owner (Envelope passes
// AssociatedData("platform.member_keys.wrapped_dek", memberID)); Unwrap with
// other associated data must fail with an error wrapping ErrInvalidCiphertext.
type KEK interface {
	ID() string
	Wrap(ctx context.Context, dek, associatedData []byte) ([]byte, error)
	Unwrap(ctx context.Context, wrapped, associatedData []byte) ([]byte, error)
}

var kekIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// LocalKEK is a KEK held in process memory. It is meant for development, tests
// and the first deployments; production uses an OpenBao Transit adapter that
// implements KEK.
type LocalKEK struct {
	id   string
	aead tink.AEAD
}

var _ KEK = (*LocalKEK)(nil)

// NewLocalKEK loads a KEK from a cleartext Tink keyset in JSON form, read by the
// caller from the secret store. Every key in the keyset must be AES256-GCM.
// The caller should clear keysetJSON after the call.
func NewLocalKEK(id string, keysetJSON []byte) (*LocalKEK, error) {
	if !kekIDRe.MatchString(id) {
		return nil, errors.New("crypto: invalid KEK id")
	}
	h, err := insecurecleartextkeyset.Read(keyset.NewJSONReader(bytes.NewReader(keysetJSON)))
	if err != nil {
		// The Tink error may quote the input; do not wrap it.
		return nil, fmt.Errorf("crypto: KEK %s: unreadable keyset", id)
	}
	if err := checkAES256GCM(h); err != nil {
		return nil, fmt.Errorf("crypto: KEK %s: %w", id, err)
	}
	a, err := aead.New(h)
	if err != nil {
		return nil, fmt.Errorf("crypto: KEK %s: %w", id, err)
	}
	return &LocalKEK{id: id, aead: a}, nil
}

// ID returns the KEK id.
func (k *LocalKEK) ID() string { return k.id }

// Wrap encrypts dek, bound to this KEK's id and to associatedData.
func (k *LocalKEK) Wrap(_ context.Context, dek, associatedData []byte) ([]byte, error) {
	if len(associatedData) == 0 {
		return nil, errors.New("crypto: wrap needs associated data")
	}
	w, err := k.aead.Encrypt(dek, k.wrapAD(associatedData))
	if err != nil {
		return nil, fmt.Errorf("crypto: wrap with KEK %s: %w", k.id, err)
	}
	return w, nil
}

// Unwrap decrypts a key wrapped by Wrap with the same associated data.
func (k *LocalKEK) Unwrap(_ context.Context, wrapped, associatedData []byte) ([]byte, error) {
	dek, err := k.aead.Decrypt(wrapped, k.wrapAD(associatedData))
	if err != nil {
		return nil, fmt.Errorf("%w: unwrap with KEK %s failed", ErrInvalidCiphertext, k.id)
	}
	return dek, nil
}

// wrapAD binds both the KEK id and the caller's associated data. The id cannot
// contain "|", so the encoding is unambiguous.
func (k *LocalKEK) wrapAD(associatedData []byte) []byte {
	return append([]byte("birge.platform.kek.v1|"+k.id+"|"), associatedData...)
}

// Keyring is the set of KEKs a deployment knows: the primary wraps new data
// keys; the others only unwrap keys created before a rotation.
type Keyring struct {
	primary KEK
	byID    map[string]KEK
}

// NewKeyring returns a keyring with primary and the retired KEKs in previous.
func NewKeyring(primary KEK, previous ...KEK) (*Keyring, error) {
	if primary == nil {
		return nil, errors.New("crypto: keyring needs a primary KEK")
	}
	r := &Keyring{primary: primary, byID: make(map[string]KEK, 1+len(previous))}
	for _, k := range append([]KEK{primary}, previous...) {
		if k == nil || k.ID() == "" {
			return nil, errors.New("crypto: keyring: KEK without id")
		}
		if _, dup := r.byID[k.ID()]; dup {
			return nil, fmt.Errorf("crypto: keyring: duplicate KEK id %s", k.ID())
		}
		r.byID[k.ID()] = k
	}
	return r, nil
}

// Primary returns the KEK that wraps new data keys.
func (r *Keyring) Primary() KEK { return r.primary }

// Get returns the KEK with the given id.
func (r *Keyring) Get(id string) (KEK, bool) {
	k, ok := r.byID[id]
	return k, ok
}

// checkAES256GCM accepts only keysets whose every key is AES-GCM with a 256-bit key.
func checkAES256GCM(h *keyset.Handle) error {
	if h.Len() == 0 {
		return errors.New("empty keyset")
	}
	for i := range h.Len() {
		e, err := h.Entry(i)
		if err != nil {
			return fmt.Errorf("keyset entry %d: %w", i, err)
		}
		k, ok := e.Key().(*aesgcm.Key)
		if !ok {
			return errors.New("keyset is not AES-GCM")
		}
		p, ok := k.Parameters().(*aesgcm.Parameters)
		if !ok || p.KeySizeInBytes() != 32 {
			return errors.New("keyset key is not 256-bit")
		}
	}
	return nil
}
