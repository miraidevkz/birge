// Package crypto is the only way Birge encrypts stored data (ADR-0009):
// per-member envelope encryption with crypto-shredding, and blind indexes for
// lookups on encrypted fields.
//
// # Envelope encryption
//
// Every member has one data-encryption key (DEK): a Tink AEAD keyset
// (AES256-GCM) created lazily on the member's first Seal and stored in
// platform.member_keys wrapped by a key-encryption key (KEK). The row records
// which KEK wrapped it (kek_id), so KEKs can be rotated: new DEKs are wrapped
// with the primary KEK of a Keyring, existing ones are unwrapped with the KEK
// named by their row (the primary or a previous one). A DEK found wrapped by
// a previous KEK is re-wrapped with the primary when it is loaded from the
// database (best effort, the DEK itself is unchanged), so after a rotation
// every active member moves to the new KEK. Members who are not active keep
// the old wrapping: a retired KEK must stay in the Keyring while any live row
// names it (docs/development.md, KEK rotation).
//
// LocalKEK holds the KEK in process memory, loaded from a cleartext Tink
// keyset that the caller reads from the secret store. Production must use
// OpenBao Transit instead, through a later adapter that implements the same
// KEK interface, so that the master key never leaves OpenBao.
//
// # Crypto-shredding
//
// Shred clears the member's wrapped DEK and sets destroyed_at. The row stays
// as a tombstone (it is never deleted), so a later Seal cannot silently create
// a new key for a deleted member: Seal and Open return ErrShredded.
//
// What shredding achieves: the member's data becomes unreadable in the live
// database immediately, and on other replicas once their cached DEK expires
// (the cache TTL). Copies in backups stay readable until those backups expire
// under the backup retention policy, because a backup also contains the
// wrapped DEK and the KEK still exists.
//
// Each wrapped DEK is bound to its member through the wrap associated data
// ("platform.member_keys.wrapped_dek|<member id>"): a wrapped_dek copied into
// another member's row fails to unwrap with ErrInvalidCiphertext.
//
// # DEK cache
//
// Unwrapped DEKs are cached in memory for a short time (default 5 minutes,
// at most 10 000 members) so that reading a page of records does not call the
// KEK once per field. The trade-off: a process memory dump exposes the cached
// keys, and after Shred other replicas can still use their cached copy of the
// DEK until it expires (the replica that ran Shred evicts it at once).
//
// # Associated data
//
// Seal and Open require associated data that names the field and the row the
// ciphertext belongs to, built with AssociatedData:
//
//	<context>.<table>.<column>|<row id>
//	e.g. "membership.members.display_name|<member id>"
//
// A ciphertext then opens only in its own column of its own row: moving it to
// another field or another row, even of the same member, fails with
// ErrInvalidCiphertext. Changing the convention for a field makes its existing
// ciphertexts unreadable, so treat the field name as part of the schema.
//
// # Logging
//
// The package never logs. Errors carry member IDs and KEK IDs only, never
// keys, keysets, plaintext, ciphertext or associated data.
package crypto

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	// ErrShredded reports that the member's data key was destroyed.
	ErrShredded = errors.New("crypto: member data key was shredded")
	// ErrInvalidCiphertext reports a ciphertext that does not open with the
	// member's key and the given associated data.
	ErrInvalidCiphertext = errors.New("crypto: invalid ciphertext or associated data")
)

// Sealer encrypts and decrypts one member's fields. Bounded contexts depend on
// this interface (through their own port), not on the implementation.
type Sealer interface {
	Seal(ctx context.Context, memberID uuid.UUID, plaintext, associatedData []byte) ([]byte, error)
	Open(ctx context.Context, memberID uuid.UUID, ciphertext, associatedData []byte) ([]byte, error)
}

// Shredder destroys a member's data key (crypto-shredding). Only the audited
// account-deletion flow calls it.
type Shredder interface {
	Shred(ctx context.Context, memberID uuid.UUID) error
}

// AssociatedData builds the associated data for a field: "<field>|<row id>",
// where field is "<context>.<table>.<column>".
func AssociatedData(field string, rowID uuid.UUID) []byte {
	return []byte(field + "|" + rowID.String())
}
