package model

// Kind classifies an Error for the API layer.
type Kind int

// Error kinds.
const (
	KindInvalid Kind = iota + 1
	KindUnauthenticated
	KindForbidden
	KindNotFound
	KindConflict
)

// Error is an expected failure of a use case. Code is stable and machine-readable;
// Title is a short English message. Neither ever carries personal data.
type Error struct {
	Kind  Kind
	Code  string
	Title string
}

func (e *Error) Error() string { return "membership: " + e.Code }

// Expected failures. Compare with errors.Is.
var (
	ErrUnauthenticated       = &Error{KindUnauthenticated, "unauthenticated", "Sign-in required."}
	ErrMemberSessionRequired = &Error{KindForbidden, "member_session_required", "A member session is required."}
	ErrForbidden             = &Error{KindForbidden, "forbidden", "Not allowed."}
	// ErrAccountDeleted refuses a session of an identity whose member was
	// deleted. It is a plain 401 to the client (same code as
	// ErrUnauthenticated), so a stale session learns nothing more.
	ErrAccountDeleted = &Error{KindUnauthenticated, "unauthenticated", "Sign-in required."}

	ErrInvalidRequest        = &Error{KindInvalid, "invalid_request", "The request is invalid."}
	ErrEmptyUpdate           = &Error{KindInvalid, "empty_update", "Nothing to update."}
	ErrInvalidDisplayName    = &Error{KindInvalid, "invalid_display_name", "The name must be 1 to 40 characters."}
	ErrInvalidLang           = &Error{KindInvalid, "invalid_lang", "Unsupported language."}
	ErrUnknownDistrict       = &Error{KindInvalid, "unknown_district", "Unknown district."}
	ErrInvalidBirthYear      = &Error{KindInvalid, "invalid_birth_year", "Invalid birth year."}
	ErrInvalidNeeds          = &Error{KindInvalid, "invalid_needs", "Invalid needs."}
	ErrUnknownNeed           = &Error{KindInvalid, "unknown_need", "Unknown need."}
	ErrInvalidIdempotencyKey = &Error{KindInvalid, "invalid_idempotency_key", "Invalid Idempotency-Key."}
	ErrSelfBlock             = &Error{KindInvalid, "self_block", "You cannot block yourself."}

	ErrMemberNotFound = &Error{KindNotFound, "member_not_found", "Member not found."}
	ErrChildNotFound  = &Error{KindNotFound, "child_not_found", "Child not found."}

	ErrChildrenLimit   = &Error{KindConflict, "children_limit", "Too many children."}
	ErrBlocksLimit     = &Error{KindConflict, "blocks_limit", "Too many blocked members."}
	ErrAlreadyVerified = &Error{KindConflict, "already_verified", "The profile is already verified."}
	ErrNotVerified     = &Error{KindConflict, "not_verified", "Member is not verified."}
	ErrSuspended       = &Error{KindConflict, "suspended", "Member is suspended."}

	// ErrMemberSuspended is returned when a suspended member's session tries
	// a member route (moderation.block_author; ADR-0019).
	ErrMemberSuspended = &Error{KindForbidden, "member_suspended", "Your account is suspended."}
)
