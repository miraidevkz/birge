// Package port declares what the membership service needs from the outside.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/platform/outbox"
)

// Encryptor seals and opens a member's personal data with that member's key
// (ADR-0009). The production implementation comes with the platform crypto package.
type Encryptor interface {
	Seal(ctx context.Context, memberID uuid.UUID, plaintext []byte) ([]byte, error)
	Open(ctx context.Context, memberID uuid.UUID, sealed []byte) ([]byte, error)
}

// Store runs a unit of work in one database transaction: committed when fn
// returns nil, rolled back otherwise.
type Store interface {
	InTx(ctx context.Context, fn func(Tx) error) error
}

// MemberRecord is a stored member; the display name is still sealed.
type MemberRecord struct {
	ID           uuid.UUID
	IdentityID   uuid.UUID
	SealedName   []byte // nil until the member sets a name
	Lang         model.Lang
	CityID       *uuid.UUID
	DistrictID   *uuid.UUID
	Verification model.Verification
	Roles        []string
}

// ProfilePatch changes the fields that are set.
type ProfilePatch struct {
	SealedName []byte
	Lang       *model.Lang
	DistrictID *uuid.UUID
}

// BlockRecord is a stored block; the blocked member's name is still sealed.
type BlockRecord struct {
	Blocked    uuid.UUID
	SealedName []byte
	Since      time.Time
}

// Tx is the store bound to one transaction.
type Tx interface {
	// MemberByIdentity returns model.ErrMemberNotFound when absent.
	MemberByIdentity(ctx context.Context, identityID uuid.UUID) (MemberRecord, error)
	// MemberByID returns model.ErrMemberNotFound when absent.
	MemberByID(ctx context.Context, id uuid.UUID) (MemberRecord, error)
	// LockMember locks the member row until the end of the transaction.
	LockMember(ctx context.Context, id uuid.UUID) error
	// InsertMember creates the member of identityID; created is false when it already exists.
	InsertMember(ctx context.Context, identityID uuid.UUID, now time.Time) (id uuid.UUID, created bool, err error)
	UpdateProfile(ctx context.Context, id uuid.UUID, p ProfilePatch, now time.Time) error
	SetVerification(ctx context.Context, id uuid.UUID, v model.Verification, now time.Time) error

	Children(ctx context.Context, memberID uuid.UUID) ([]model.Child, error)
	CountChildren(ctx context.Context, memberID uuid.UUID) (int, error)
	// ChildByIdempotencyKey returns model.ErrChildNotFound when absent.
	ChildByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Child, error)
	InsertChild(ctx context.Context, memberID, key uuid.UUID, c model.ChildInput, now time.Time) (model.Child, error)
	// UpdateChild returns model.ErrChildNotFound unless the child belongs to memberID.
	UpdateChild(ctx context.Context, memberID, childID uuid.UUID, c model.ChildInput, now time.Time) (model.Child, error)
	// DeleteChild reports whether a child of memberID was removed.
	DeleteChild(ctx context.Context, memberID, childID uuid.UUID) (bool, error)

	// InsertPendingVerification creates a pending request; created is false while one is pending.
	InsertPendingVerification(ctx context.Context, memberID uuid.UUID, now time.Time) (requestID uuid.UUID, created bool, err error)

	// InsertBlock reports false when the block already exists.
	InsertBlock(ctx context.Context, blocker, blocked uuid.UUID, now time.Time) (bool, error)
	DeleteBlock(ctx context.Context, blocker, blocked uuid.UUID) error
	CountBlocks(ctx context.Context, blocker uuid.UUID) (int, error)
	Blocks(ctx context.Context, blocker uuid.UUID, limit int) ([]BlockRecord, error)
	// IsBlocked reports a block between a and b in either direction.
	IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error)

	DistrictExists(ctx context.Context, id uuid.UUID) (bool, error)
	// CountKnownNeeds returns how many of the distinct codes exist in the reference.
	CountKnownNeeds(ctx context.Context, codes []string) (int, error)
	Cities(ctx context.Context) ([]model.City, error)
	Needs(ctx context.Context) ([]string, error)

	// Publish stores e in the platform outbox in this transaction.
	Publish(ctx context.Context, now time.Time, e outbox.Event) error
}
