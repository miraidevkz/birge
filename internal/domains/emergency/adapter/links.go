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

// InsertSosLink stores a new external SOS link (keyed hash only).
func (t *postgresTx) InsertSosLink(ctx context.Context, l model.SosLink) error {
	if err := t.q.InsertSosLink(ctx, emergencystore.InsertSosLinkParams{
		ID: l.ID, TokenHash: l.TokenHash, SosID: l.SosID, ContactID: l.ContactID, OwnerID: l.OwnerID,
		CreatedAt: l.CreatedAt, ExpiresAt: l.ExpiresAt,
	}); err != nil {
		// The error never carries the hash: pgx reports the constraint, not values.
		return fmt.Errorf("insert sos link: %w", err)
	}
	return nil
}

// RevokeSosContactLinks revokes the unrevoked links of the SOS and contact.
func (t *postgresTx) RevokeSosContactLinks(ctx context.Context, sosID, contactID uuid.UUID, at time.Time) (int64, error) {
	n, err := t.q.RevokeSosContactLinks(ctx, emergencystore.RevokeSosContactLinksParams{RevokedAt: &at, SosID: sosID, ContactID: contactID})
	if err != nil {
		return 0, fmt.Errorf("revoke sos links: %w", err)
	}
	return n, nil
}

// GetLiveSosLink returns the live link with tokenHash and its SOS, or
// model.ErrNotFound for every failure (one query).
func (t *postgresTx) GetLiveSosLink(ctx context.Context, tokenHash []byte, now time.Time) (model.LiveSosLink, error) {
	r, err := t.q.GetLiveSosLink(ctx, emergencystore.GetLiveSosLinkParams{TokenHash: tokenHash, Now: now})
	if err != nil {
		return model.LiveSosLink{}, notFound(err, "get sos link")
	}
	return model.LiveSosLink{
		Link: model.SosLink{
			ID: r.ID, SosID: r.SosID, ContactID: r.ContactID, OwnerID: r.OwnerID,
			CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		},
		Sos: model.Sos{
			ID: r.SosID, MemberID: r.OwnerID, Type: r.Type, Text: r.Content, Status: r.Status,
			CreatedAt: r.SosCreatedAt, ClosesAt: r.ClosesAt,
		},
	}, nil
}

// DeleteExpiredSosLink deletes the link when it expired at or before cutoff.
func (t *postgresTx) DeleteExpiredSosLink(ctx context.Context, linkID uuid.UUID, cutoff time.Time) (int64, error) {
	n, err := t.q.DeleteExpiredSosLink(ctx, emergencystore.DeleteExpiredSosLinkParams{ID: linkID, Cutoff: cutoff})
	if err != nil {
		return 0, fmt.Errorf("delete expired sos link: %w", err)
	}
	return n, nil
}

// ScheduleSosLinkPurge schedules emergency.SosLinkPurge (dedup key = link id).
func (t *postgresTx) ScheduleSosLinkPurge(ctx context.Context, linkID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerSosLinkPurge,
		SubjectID: linkID,
		DedupKey:  linkID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
}
