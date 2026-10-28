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
	ID              uuid.UUID
	IdentityID      uuid.UUID
	SealedName      []byte // nil until the member sets a name
	Lang            model.Lang
	CityID          *uuid.UUID
	DistrictID      *uuid.UUID
	Verification    model.Verification
	VerifiedAt      *time.Time
	Roles           []string
	SuspendedAt     *time.Time
	SuspendedReason string
	// Deleted marks the tombstone of a deleted member: only ID and
	// IdentityID are meaningful; the identity must never get a new member.
	Deleted bool
}

// MemberEraser erases a member's membership data on account deletion.
type MemberEraser interface {
	// EraseMember, in one transaction: deletes the member's children,
	// verification requests and blocks in both directions, turns the member
	// row into a tombstone (identity_id kept, everything else cleared),
	// schedules model.TimerDeleteIdentity once and records the audit entry
	// once. An unknown member is not an error. Repeating it changes nothing.
	EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error
}

// IdentityDeleter deletes an identity at the identity provider (Kratos admin API).
type IdentityDeleter interface {
	// DeleteIdentity returns nil when the identity is gone, including when
	// it did not exist (404).
	DeleteIdentity(ctx context.Context, identityID uuid.UUID) error
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

// StaffMemberListFilter filters and pages staff member queries.
type StaffMemberListFilter struct {
	Status     string // "verified" or "suspended"
	DistrictID *uuid.UUID
	CursorTime *time.Time
	CursorID   *uuid.UUID
	Limit      int32
}

// StaffMemberRecord is a member row returned for staff queries; display name is sealed.
type StaffMemberRecord struct {
	ID           uuid.UUID
	SealedName   []byte
	DistrictID   *uuid.UUID
	Verification model.Verification
	VerifiedAt   *time.Time
	SuspendedAt  *time.Time
	Roles        []string
}

// Tx is the store bound to one transaction.
type Tx interface {
	// MemberByIdentity returns model.ErrMemberNotFound when absent.
	MemberByIdentity(ctx context.Context, identityID uuid.UUID) (MemberRecord, error)
	// MemberByID returns model.ErrMemberNotFound when absent.
	MemberByID(ctx context.Context, id uuid.UUID) (MemberRecord, error)
	// ListVerifiedInDistrict returns the IDs of verified members in districtID.
	ListVerifiedInDistrict(ctx context.Context, districtID uuid.UUID) ([]uuid.UUID, error)
	// ListStaffMembers returns members by status (verified or suspended) with keyset paging.
	ListStaffMembers(ctx context.Context, f StaffMemberListFilter) ([]StaffMemberRecord, error)
	// LockMember locks the member row until the end of the transaction.
	LockMember(ctx context.Context, id uuid.UUID) error
	// InsertMember creates the member of identityID; created is false when it already exists.
	InsertMember(ctx context.Context, identityID uuid.UUID, now time.Time) (id uuid.UUID, created bool, err error)
	// UpdateProfile applies the set fields of p; returns model.ErrMemberNotFound when id is absent.
	UpdateProfile(ctx context.Context, id uuid.UUID, p ProfilePatch, now time.Time) error
	// SetVerification writes the member's verification state.
	SetVerification(ctx context.Context, id uuid.UUID, v model.Verification, now time.Time) error
	// SetRoles updates the member's roles.
	SetRoles(ctx context.Context, id uuid.UUID, roles []string, now time.Time) error
	// SetSuspension sets or clears staff suspension (both fields together).
	SetSuspension(ctx context.Context, id uuid.UUID, at *time.Time, reason string, now time.Time) error

	// GetVerificationRequest loads a verification request by ID.
	GetVerificationRequest(ctx context.Context, id uuid.UUID, forUpdate bool) (model.VerificationRequest, error)
	// DecideVerificationRequest marks a pending request approved or rejected.
	// updated is false when it was not pending.
	DecideVerificationRequest(ctx context.Context, id uuid.UUID, status, rejectReason string, now time.Time) (updated bool, err error)

	// Children lists the member's child cards.
	Children(ctx context.Context, memberID uuid.UUID) ([]model.Child, error)
	// CountChildren returns how many child cards the member has.
	CountChildren(ctx context.Context, memberID uuid.UUID) (int, error)
	// ChildByIdempotencyKey returns model.ErrChildNotFound when absent.
	ChildByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Child, error)
	// InsertChild creates a child card keyed by the request Idempotency-Key.
	InsertChild(ctx context.Context, memberID, key uuid.UUID, c model.ChildInput, now time.Time) (model.Child, error)
	// UpdateChild returns model.ErrChildNotFound unless the child belongs to memberID.
	UpdateChild(ctx context.Context, memberID, childID uuid.UUID, c model.ChildInput, now time.Time) (model.Child, error)
	// DeleteChild reports whether a child of memberID was removed.
	DeleteChild(ctx context.Context, memberID, childID uuid.UUID) (bool, error)

	// InsertPendingVerification creates a pending request; created is false while one is pending.
	InsertPendingVerification(ctx context.Context, memberID uuid.UUID, now time.Time) (requestID uuid.UUID, created bool, err error)

	// InsertBlock reports false when the block already exists.
	InsertBlock(ctx context.Context, blocker, blocked uuid.UUID, now time.Time) (bool, error)
	// DeleteBlock removes a block if present (idempotent).
	DeleteBlock(ctx context.Context, blocker, blocked uuid.UUID) error
	// CountBlocks returns how many members blocker has blocked.
	CountBlocks(ctx context.Context, blocker uuid.UUID) (int, error)
	// Blocks lists members blocked by blocker, newest first, capped at limit.
	Blocks(ctx context.Context, blocker uuid.UUID, limit int) ([]BlockRecord, error)
	// IsBlocked reports a block between a and b in either direction.
	IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error)

	// DistrictExists reports whether id is a known district.
	DistrictExists(ctx context.Context, id uuid.UUID) (bool, error)
	// CountKnownNeeds returns how many of the distinct codes exist in the reference.
	CountKnownNeeds(ctx context.Context, codes []string) (int, error)
	// Cities returns the deployment's cities with their districts.
	Cities(ctx context.Context) ([]model.City, error)
	// Needs returns the deployment's needs codes.
	Needs(ctx context.Context) ([]string, error)

	// Publish stores e in the platform outbox in this transaction.
	Publish(ctx context.Context, now time.Time, e outbox.Event) error
	// MarkProcessed records that consumer handled eventID; newly is false for a duplicate.
	MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (newly bool, err error)
	// Audit writes one platform.audit_log row in this transaction.
	Audit(ctx context.Context, actorID uuid.UUID, actorRole, action, objectType string, objectID uuid.UUID, reason string) error
}
