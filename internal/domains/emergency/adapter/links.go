package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	emergencystore "birge/internal/domains/emergency/store"
	"birge/internal/platform/timers"
)

// InsertPlanLink stores a new external plan link (keyed hash only).
func (t *postgresTx) InsertPlanLink(ctx context.Context, l model.PlanLink) error {
	if err := t.q.InsertPlanLink(ctx, emergencystore.InsertPlanLinkParams{
		ID: l.ID, TokenHash: l.TokenHash, ContactID: l.ContactID, OwnerID: l.OwnerID,
		SentAt: l.SentAt, CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt,
	}); err != nil {
		// The error never carries the hash: pgx reports the constraint, not values.
		return fmt.Errorf("insert plan link: %w", err)
	}
	return nil
}

// RevokeContactPlanLinks revokes the contact's unrevoked links.
func (t *postgresTx) RevokeContactPlanLinks(ctx context.Context, contactID uuid.UUID, at time.Time) (int64, error) {
	n, err := t.q.RevokeContactPlanLinks(ctx, emergencystore.RevokeContactPlanLinksParams{RevokedAt: &at, ContactID: contactID})
	if err != nil {
		return 0, fmt.Errorf("revoke contact plan links: %w", err)
	}
	return n, nil
}

// RevokeOwnerPlanLinks revokes every unrevoked link of the owner's contacts.
func (t *postgresTx) RevokeOwnerPlanLinks(ctx context.Context, ownerID uuid.UUID, at time.Time) (int64, error) {
	n, err := t.q.RevokeOwnerPlanLinks(ctx, emergencystore.RevokeOwnerPlanLinksParams{RevokedAt: &at, OwnerID: ownerID})
	if err != nil {
		return 0, fmt.Errorf("revoke owner plan links: %w", err)
	}
	return n, nil
}

// GetLivePlanLink returns the live link with tokenHash, or model.ErrNotFound.
func (t *postgresTx) GetLivePlanLink(ctx context.Context, tokenHash []byte, now time.Time) (model.PlanLink, error) {
	r, err := t.q.GetLivePlanLink(ctx, emergencystore.GetLivePlanLinkParams{TokenHash: tokenHash, Now: now})
	if err != nil {
		return model.PlanLink{}, notFound(err, "get plan link")
	}
	return model.PlanLink{
		ID: r.ID, TokenHash: r.TokenHash, ContactID: r.ContactID, OwnerID: r.OwnerID,
		SentAt: r.SentAt, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt, RevokedAt: r.RevokedAt,
	}, nil
}

// DeleteExpiredPlanLink deletes the link when it expired at or before cutoff.
func (t *postgresTx) DeleteExpiredPlanLink(ctx context.Context, linkID uuid.UUID, cutoff time.Time) (int64, error) {
	n, err := t.q.DeleteExpiredPlanLink(ctx, emergencystore.DeleteExpiredPlanLinkParams{ID: linkID, Cutoff: cutoff})
	if err != nil {
		return 0, fmt.Errorf("delete expired plan link: %w", err)
	}
	return n, nil
}

// SchedulePlanLinkPurge schedules emergency.PlanLinkPurge (dedup key = link id).
func (t *postgresTx) SchedulePlanLinkPurge(ctx context.Context, linkID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerPlanLinkPurge,
		SubjectID: linkID,
		DedupKey:  linkID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
}
