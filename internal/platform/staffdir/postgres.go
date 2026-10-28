package staffdir

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	"birge/internal/platform/staffauth"
	"birge/internal/platform/store"
)

// PostgresStore implements Store and staffauth.AccountRecorder on
// platform.staff_accounts and platform.audit_log.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

var _ staffauth.AccountRecorder = (*PostgresStore)(nil)

// RecordStaffAccount upserts the account (staff auth middleware). An empty
// display name is stored as NULL. The error never quotes the name.
func (s *PostgresStore) RecordStaffAccount(ctx context.Context, a staffauth.Account) error {
	var name *string
	if a.DisplayName != "" {
		name = &a.DisplayName
	}
	roles := a.Roles
	if roles == nil {
		roles = []string{}
	}
	if err := store.New(s.Pool).UpsertStaffAccount(ctx, store.UpsertStaffAccountParams{
		StaffID: a.StaffID, DisplayName: name, Roles: roles, SeenAt: a.SeenAt,
	}); err != nil {
		return fmt.Errorf("staffdir: record account: %w", err)
	}
	return nil
}

// auditActorRoles are the actor_role values that mark a staff action: the
// four staff roles, plus "staff", which some contexts (membership, chat)
// record when they know only that the caller was staff. Members, mentors and
// the system are never listed.
var auditActorRoles = append(slices.Clone(staffauth.Roles), "staff")

// ReadAuditLog selects one page and then writes read, both in one
// transaction. The read entry is inserted after the select, so a page never
// lists the read that produced it.
func (s *PostgresStore) ReadAuditLog(ctx context.Context, q AuditQuery, read audit.Entry) ([]AuditEntry, error) {
	var out []AuditEntry
	err := db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		qs := store.New(tx)
		rows, err := qs.ListStaffAuditEntries(ctx, store.ListStaffAuditEntriesParams{
			StaffRoles: auditActorRoles,
			ActorID:    optUUID(q.ActorID),
			ObjectType: optString(q.ObjectType),
			ObjectID:   optUUID(q.ObjectID),
			Action:     optString(q.Action),
			FromAt:     q.From,
			ToAt:       q.To,
			CursorAt:   q.CursorAt,
			CursorID:   optUUID(q.CursorID),
			PageLimit:  int32(q.Limit), //nolint:gosec // bounded by AuditPageSize+1
		})
		if err != nil {
			return fmt.Errorf("staffdir: list audit entries: %w", err)
		}
		out = make([]AuditEntry, 0, len(rows))
		for _, r := range rows {
			e := AuditEntry{
				ID: r.ID, OccurredAt: r.OccurredAt, ActorID: r.ActorID, ActorRole: r.ActorRole,
				Action: r.Action, ObjectType: r.ObjectType,
			}
			if r.ActorName != nil {
				e.ActorName = *r.ActorName
			}
			if r.ObjectID != nil {
				e.ObjectID = *r.ObjectID
			}
			if r.Reason != nil {
				e.Reason = *r.Reason
			}
			out = append(out, e)
		}
		return audit.Record(ctx, qs, read)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListAccounts returns up to limit accounts, most recently seen first.
func (s *PostgresStore) ListAccounts(ctx context.Context, limit int) ([]Account, error) {
	rows, err := store.New(s.Pool).ListStaffAccounts(ctx, int32(limit)) //nolint:gosec // bounded by MaxAccounts
	if err != nil {
		return nil, fmt.Errorf("staffdir: list accounts: %w", err)
	}
	out := make([]Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, account(r))
	}
	return out, nil
}

// GetAccount returns the account with that staff ID, or ErrNotFound.
func (s *PostgresStore) GetAccount(ctx context.Context, id uuid.UUID) (Account, error) {
	r, err := store.New(s.Pool).GetStaffAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("staffdir: get account: %w", err)
	}
	return account(r), nil
}

// CountActions counts id's staff entries per action since since.
func (s *PostgresStore) CountActions(ctx context.Context, id uuid.UUID, since time.Time, limit int) ([]ActionCount, error) {
	rows, err := store.New(s.Pool).CountStaffActionsSince(ctx, store.CountStaffActionsSinceParams{
		ActorID: id, Since: since, StaffRoles: auditActorRoles, RowLimit: int32(limit), //nolint:gosec // bounded by MaxActionCounts
	})
	if err != nil {
		return nil, fmt.Errorf("staffdir: count actions: %w", err)
	}
	out := make([]ActionCount, 0, len(rows))
	for _, r := range rows {
		out = append(out, ActionCount{Action: r.Action, Count: int(r.N)})
	}
	return out, nil
}

func account(r store.PlatformStaffAccount) Account {
	a := Account{ID: r.StaffID, Roles: r.Roles, FirstSeenAt: r.FirstSeenAt, LastSeenAt: r.LastSeenAt}
	if r.DisplayName != nil {
		a.DisplayName = *r.DisplayName
	}
	if a.Roles == nil {
		a.Roles = []string{}
	}
	return a
}

func optUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
