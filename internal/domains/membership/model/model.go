// Package model holds the membership types and their invariants. No I/O.
package model

import (
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Limits from the contract (contracts/openapi/birge.yaml, tag membership).
const (
	MaxDisplayNameRunes = 40
	MaxChildren         = 10
	MaxNeeds            = 10
	MaxBlocks           = 1000
	MinBirthYear        = 1990
	MaxBirthYear        = 2100
)

// Event types published by this context (contracts/events/membership.*.v1.json).
const (
	EventMemberRegistered      = "membership.MemberRegistered"
	EventVerificationRequested = "membership.VerificationRequested"
	EventMemberVerified        = "membership.MemberVerified"
	EventModerationDecided     = "moderation.Decided"
)

// ConsumerModerationDecided is the platform.processed_events consumer name.
const ConsumerModerationDecided = "membership.moderation.Decided"

// Audit codes for staff suspension and system application of decisions.
const (
	AuditObjectMember       = "membership.member"
	AuditObjectVerification = "membership.verification_request"
	AuditSuspend            = "membership.member.suspend"
	AuditLiftSuspension     = "membership.member.lift_suspension"
	AuditVerifyApprove      = "membership.verification.approve"
	AuditVerifyReject       = "membership.verification.reject"
	ActorRoleSystem         = "system"
	ActorRoleStaff          = "staff"
)

// Verification request statuses.
const (
	VerificationRequestPending  = "pending"
	VerificationRequestApproved = "approved"
	VerificationRequestRejected = "rejected"
)

var codeRe = regexp.MustCompile(`^[a-z_]{2,40}$`)

// Lang is the member's interface language.
type Lang string

// Languages of the MVP.
const (
	LangKK Lang = "kk"
	LangRU Lang = "ru"
	LangEN Lang = "en"
)

// Valid reports whether l is a supported language.
func (l Lang) Valid() bool { return l == LangKK || l == LangRU || l == LangEN }

// Verification is the member's verification state.
type Verification string

// Verification states.
const (
	VerificationNew      Verification = "new"
	VerificationPending  Verification = "pending"
	VerificationVerified Verification = "verified"
)

// Member is a member profile. DisplayName is personal data: it is never logged
// and never put into events.
type Member struct {
	ID              uuid.UUID
	IdentityID      uuid.UUID
	DisplayName     string // empty until the member sets one
	Lang            Lang
	CityID          *uuid.UUID
	DistrictID      *uuid.UUID
	Verification    Verification
	Roles           []string
	Children        []Child
	SuspendedAt     *time.Time
	SuspendedReason string
}

// Suspended reports whether the member is currently suspended by staff.
func (m Member) Suspended() bool { return m.SuspendedAt != nil }

// Suspension is the staff view of a member's suspension state.
type Suspension struct {
	MemberID    uuid.UUID
	Suspended   bool
	SuspendedAt *time.Time
	Reason      string
}

// VerificationRequest is one moderation object for a verification call.
type VerificationRequest struct {
	ID           uuid.UUID
	MemberID     uuid.UUID
	Status       string
	RejectReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ModerationDecidedData is the payload of moderation.Decided v1.
type ModerationDecidedData struct {
	ItemID      uuid.UUID `json:"item_id"`
	Kind        string    `json:"kind"`
	ObjectID    uuid.UUID `json:"object_id"`
	Approve     bool      `json:"approve"`
	Reason      string    `json:"reason,omitempty"`
	BlockAuthor bool      `json:"block_author"`
	LegalHold   bool      `json:"legal_hold"`
}

// ModerationKindVerification is the Decided kind this context applies.
const ModerationKindVerification = "verification"

// LogValue keeps personal data out of logs when a Member is logged by accident.
func (m Member) LogValue() slog.Value {
	return slog.GroupValue(slog.String("member_id", m.ID.String()))
}

// Child is a child of a member. Children carry no names.
type Child struct {
	ID        uuid.UUID
	BirthYear int
	Needs     []string
}

// ChildInput is a new or replaced child card.
type ChildInput struct {
	BirthYear int
	Needs     []string // codes from the needs reference; empty means "prefer not to say"
}

// Normalize validates c and returns it with needs deduplicated and sorted.
func (c ChildInput) Normalize() (ChildInput, error) {
	if c.BirthYear < MinBirthYear || c.BirthYear > MaxBirthYear {
		return ChildInput{}, ErrInvalidBirthYear
	}
	if len(c.Needs) > MaxNeeds {
		return ChildInput{}, ErrInvalidNeeds
	}
	needs := make([]string, 0, len(c.Needs))
	for _, n := range c.Needs {
		if !codeRe.MatchString(n) {
			return ChildInput{}, ErrInvalidNeeds
		}
		needs = append(needs, n)
	}
	slices.Sort(needs)
	return ChildInput{BirthYear: c.BirthYear, Needs: slices.Compact(needs)}, nil
}

// ProfileUpdate changes the fields that are set.
type ProfileUpdate struct {
	DisplayName *string
	Lang        *Lang
	DistrictID  *uuid.UUID
}

// Normalize validates u and returns it with the display name trimmed.
func (u ProfileUpdate) Normalize() (ProfileUpdate, error) {
	if u.DisplayName == nil && u.Lang == nil && u.DistrictID == nil {
		return ProfileUpdate{}, ErrEmptyUpdate
	}
	out := u
	if u.DisplayName != nil {
		name, err := NormalizeDisplayName(*u.DisplayName)
		if err != nil {
			return ProfileUpdate{}, err
		}
		out.DisplayName = &name
	}
	if u.Lang != nil && !u.Lang.Valid() {
		return ProfileUpdate{}, ErrInvalidLang
	}
	if u.DistrictID != nil && *u.DistrictID == uuid.Nil {
		return ProfileUpdate{}, ErrUnknownDistrict
	}
	return out, nil
}

// NormalizeDisplayName trims s and checks it is 1..40 printable characters.
func NormalizeDisplayName(s string) (string, error) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if !utf8.ValidString(s) || n < 1 || n > MaxDisplayNameRunes {
		return "", ErrInvalidDisplayName
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return "", ErrInvalidDisplayName
		}
	}
	return s, nil
}

// BlockedMember is a member blocked by the viewer.
type BlockedMember struct {
	MemberID    uuid.UUID
	DisplayName string
	Since       time.Time
}

// City is a city of the deployment with its districts.
type City struct {
	ID        uuid.UUID
	Code      string
	Districts []District
}

// District is a district of a city.
type District struct {
	ID   uuid.UUID
	Code string
}

// ReferenceCitySeed is a city code with district codes for a first-start seed.
// IDs are assigned by the database; SeedReferenceIfEmpty never overwrites.
type ReferenceCitySeed struct {
	Code      string
	Districts []string
}

// ReferenceSeed is the membership reference data inserted when tables are empty.
type ReferenceSeed struct {
	Cities []ReferenceCitySeed
	Needs  []string
}
