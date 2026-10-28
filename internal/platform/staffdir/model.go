package staffdir

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Errors returned by Service; Server maps them to problem responses.
var (
	// ErrUnauthenticated: no staff principal in the context (401).
	ErrUnauthenticated = errors.New("staffdir: unauthenticated")
	// ErrForbidden: a staff principal without the admin role or MFA (403).
	ErrForbidden = errors.New("staffdir: forbidden")
	// ErrNotFound: no staff account with that ID (404 "not_found").
	ErrNotFound = errors.New("staffdir: not found")
	// ErrInvalid: a malformed cursor or a reversed time range (400).
	ErrInvalid = errors.New("staffdir: invalid request")
)

const (
	// AuditPageSize is the number of audit entries per page.
	AuditPageSize = 50
	// MaxAccounts is the most accounts listStaffAccounts returns (the
	// contract's maxItems).
	MaxAccounts = 1000
	// MaxActionCounts is the most action codes on a staff card (the
	// contract's maxItems for actions30d).
	MaxActionCounts = 200
	// CardWindow is the period of a staff card's action counts.
	CardWindow = 30 * 24 * time.Hour
	// MaxCursorLen is the longest accepted cursor (the contract's maxLength).
	MaxCursorLen = 128
)

// Audit codes of the entry each read of the log writes.
const (
	AuditActionRead = "audit.read"
	AuditObjectLog  = "platform.audit_log"
)

// Account is a staff account as recorded by the staff auth middleware.
type Account struct {
	ID          uuid.UUID
	DisplayName string // "" when the IdP sent no usable name
	Roles       []string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// ActionCount is how many times a staff member did one action.
type ActionCount struct {
	Action string
	Count  int
}

// Card is a staff account with its action counts over CardWindow, most
// frequent first.
type Card struct {
	Account Account
	Actions []ActionCount
}

// AuditFilter selects audit entries. Zero values mean "any"; From and To are
// inclusive. Cursor is the opaque value of a previous page's NextCursor.
type AuditFilter struct {
	ActorID    uuid.UUID
	ObjectType string
	ObjectID   uuid.UUID
	Action     string
	From       *time.Time
	To         *time.Time
	Cursor     string
}

// AuditEntry is one staff entry of the audit log.
type AuditEntry struct {
	ID         uuid.UUID
	OccurredAt time.Time
	ActorID    uuid.UUID
	ActorRole  string
	ActorName  string // "" when the account is unknown or has no name
	Action     string
	ObjectType string
	ObjectID   uuid.UUID // uuid.Nil when the entry has none
	Reason     string
}

// AuditPage is one page of entries, newest first; NextCursor is "" on the
// last page.
type AuditPage struct {
	Items      []AuditEntry
	NextCursor string
}

// AuditQuery is what the store runs for one page: the filter with the cursor
// decoded and the page limit set.
type AuditQuery struct {
	ActorID    uuid.UUID
	ObjectType string
	ObjectID   uuid.UUID
	Action     string
	From       *time.Time
	To         *time.Time
	CursorAt   *time.Time
	CursorID   uuid.UUID
	Limit      int
}
