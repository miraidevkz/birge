package staffdir

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"birge/internal/platform/audit"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/staffauth"
)

// Store is the persistence Service needs (PostgresStore in production).
type Store interface {
	// ReadAuditLog returns the entries q selects and records read in the
	// same transaction: when the audit entry cannot be written, nothing is
	// returned.
	ReadAuditLog(ctx context.Context, q AuditQuery, read audit.Entry) ([]AuditEntry, error)
	// ListAccounts returns up to limit accounts, most recently seen first.
	ListAccounts(ctx context.Context, limit int) ([]Account, error)
	// GetAccount returns the account, or ErrNotFound.
	GetAccount(ctx context.Context, id uuid.UUID) (Account, error)
	// CountActions counts id's staff entries per action since since, most
	// frequent first, at most limit codes.
	CountActions(ctx context.Context, id uuid.UUID, since time.Time, limit int) ([]ActionCount, error)
}

// Service implements the staff directory use cases. Store is required;
// Clock and Logger default to the system clock and slog.Default().
type Service struct {
	Store  Store
	Clock  clock.Clock
	Logger *slog.Logger
}

// ListAuditEntries returns one page of staff audit entries, newest first.
// Admin with MFA only. The read itself is audited (AuditActionRead), with the
// admin role as actor role so it shows in the log it reads.
func (s *Service) ListAuditEntries(ctx context.Context, f AuditFilter) (AuditPage, error) {
	p, err := requireAdmin(ctx)
	if err != nil {
		return AuditPage{}, err
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		return AuditPage{}, fmt.Errorf("%w: from is after to", ErrInvalid)
	}
	q := AuditQuery{
		ActorID: f.ActorID, ObjectType: f.ObjectType, ObjectID: f.ObjectID, Action: f.Action,
		From: f.From, To: f.To, Limit: AuditPageSize + 1,
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return AuditPage{}, err
		}
		q.CursorAt, q.CursorID = &at, id
	}
	read := audit.Entry{
		ActorID: p.IdentityID, ActorRole: staffauth.RoleAdmin,
		Action: AuditActionRead, ObjectType: AuditObjectLog,
	}
	items, err := s.Store.ReadAuditLog(ctx, q, read)
	if err != nil {
		return AuditPage{}, err
	}
	var page AuditPage
	if len(items) > AuditPageSize {
		items = items[:AuditPageSize]
		last := items[len(items)-1]
		page.NextCursor = encodeCursor(last.OccurredAt, last.ID)
	}
	page.Items = items
	s.log().InfoContext(ctx, "staff audit log read", "event", "StaffAuditRead",
		"staff_id", p.IdentityID, "count", len(items))
	return page, nil
}

// ListStaffAccounts returns every staff account (up to MaxAccounts), most
// recently active first. Admin with MFA only.
func (s *Service) ListStaffAccounts(ctx context.Context) ([]Account, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	return s.Store.ListAccounts(ctx, MaxAccounts)
}

// GetStaffAccount returns one account with its action counts over
// CardWindow. Admin with MFA only; ErrNotFound for an unknown ID.
func (s *Service) GetStaffAccount(ctx context.Context, id uuid.UUID) (Card, error) {
	if _, err := requireAdmin(ctx); err != nil {
		return Card{}, err
	}
	acc, err := s.Store.GetAccount(ctx, id)
	if err != nil {
		return Card{}, err
	}
	counts, err := s.Store.CountActions(ctx, id, s.now().Add(-CardWindow), MaxActionCounts)
	if err != nil {
		return Card{}, err
	}
	return Card{Account: acc, Actions: counts}, nil
}

// requireAdmin authorizes a staff call with the admin role and MFA. Anything
// else is refused: deny by default.
func requireAdmin(ctx context.Context) (auth.Principal, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || !p.Staff || p.IdentityID == uuid.Nil {
		return auth.Principal{}, ErrUnauthenticated
	}
	if !p.MFA || !slices.Contains(p.Roles, staffauth.RoleAdmin) {
		return auth.Principal{}, ErrForbidden
	}
	return p, nil
}

func (s *Service) now() time.Time {
	if s.Clock == nil {
		return time.Now().UTC()
	}
	return s.Clock.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// encodeCursor makes the opaque cursor of the last entry on a page. PostgreSQL
// keeps microseconds, so the time is encoded in microseconds and compares
// exactly on the next page.
func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMicro(), 10) + "." + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	bad := fmt.Errorf("%w: cursor", ErrInvalid)
	if len(c) > MaxCursorLen {
		return time.Time{}, uuid.Nil, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, bad
	}
	us, idStr, ok := strings.Cut(string(raw), ".")
	if !ok {
		return time.Time{}, uuid.Nil, bad
	}
	n, err := strconv.ParseInt(us, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, bad
	}
	id, err := uuid.Parse(idStr)
	if err != nil || len(idStr) != 36 {
		return time.Time{}, uuid.Nil, bad
	}
	return time.UnixMicro(n).UTC(), id, nil
}
