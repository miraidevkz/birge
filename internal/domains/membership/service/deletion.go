package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/platform/clock"
)

// Deletion erases the membership data of a member on account deletion
// (privacy.DataDeletionRequested, AGENTS.md N6) and deletes her identity at
// the identity provider afterwards (timer model.TimerDeleteIdentity). It is
// called by the system, never on behalf of a principal.
//
// The member row stays as a tombstone with its identity ID only: the
// session middleware refuses the identity (model.ErrAccountDeleted) and
// lazy creation never makes a new member for it, even after the identity is
// deleted and an old session token is replayed.
type Deletion struct {
	Store      port.MemberEraser
	Identities port.IdentityDeleter
	Clock      clock.Clock
	Logger     *slog.Logger
}

// DeleteMember erases memberID's membership data. Idempotent.
func (d *Deletion) DeleteMember(ctx context.Context, memberID uuid.UUID) error {
	if memberID == uuid.Nil {
		return errors.New("membership: delete member: member id is required")
	}
	if err := d.Store.EraseMember(ctx, memberID, d.Clock.Now().UTC()); err != nil {
		return fmt.Errorf("membership: delete member: %w", err)
	}
	d.log().Info("member data deleted", "event", "MemberDataDeleted", "context", model.BoundedContext, "member_id", memberID)
	return nil
}

// DeleteIdentity deletes the identity at the identity provider. A missing
// identity counts as done; any other failure is returned so the timer retries.
func (d *Deletion) DeleteIdentity(ctx context.Context, identityID uuid.UUID) error {
	if d.Identities == nil {
		return errors.New("membership: delete identity: identity provider not configured")
	}
	if err := d.Identities.DeleteIdentity(ctx, identityID); err != nil {
		return fmt.Errorf("membership: delete identity: %w", err)
	}
	d.log().Info("identity deleted", "event", "IdentityDeleted", "identity_id", identityID)
	return nil
}

func (d *Deletion) log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
