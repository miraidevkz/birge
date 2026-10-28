package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/sharing/model"
	sharingstore "birge/internal/domains/sharing/store"
	"birge/internal/platform/db"
)

// DemoListing is one DEMO item listing for a fresh deployment, without a
// photo. IdempotencyKey must be stable per listing (wiring derives it from the
// preset slug), so two concurrent seeds resolve to one row per listing through
// the (author_id, idempotency_key) unique index.
type DemoListing struct {
	IdempotencyKey uuid.UUID
	Type           string
	Category       string
	Title          string
	Description    string
	DistrictID     uuid.UUID
}

// SeedDemoIfEmpty inserts the DEMO listings by authorID when sharing.listings
// is empty, and takes each one through the normal lifecycle: approved, active
// until now+ttl, with a sharing.AutoClose timer at that time, so the rows obey
// every listing invariant. It never overwrites and publishes no events or
// notifications (DEMO listings skip moderation on purpose). seeded is true
// only when this call inserted at least one listing.
func SeedDemoIfEmpty(ctx context.Context, pool *pgxpool.Pool, authorID uuid.UUID, listings []DemoListing, ttl time.Duration, now time.Time) (seeded bool, err error) {
	if len(listings) == 0 {
		return false, nil
	}
	if ttl <= 0 {
		return false, fmt.Errorf("sharing: demo seed needs a positive ttl")
	}
	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		empty, err := sharingstore.New(tx).SharingListingsEmpty(ctx)
		if err != nil {
			return fmt.Errorf("sharing: empty check: %w", err)
		}
		if !empty {
			return nil
		}
		t := Bind(tx)
		for i, l := range listings {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("sharing: listing id: %w", err)
			}
			// Distinct created_at keeps the feed order (newest first) stable
			// and equal to the preset order.
			created, inserted, err := t.InsertListing(ctx, model.Listing{
				ID: id, AuthorID: authorID, IdempotencyKey: l.IdempotencyKey, Type: l.Type,
				Category: l.Category, Title: l.Title, Description: l.Description,
				DistrictID: l.DistrictID, Status: model.ListingPending,
				CreatedAt: now.Add(-time.Duration(i) * time.Minute),
			})
			if err != nil {
				return err
			}
			if !inserted {
				continue
			}
			if _, _, err := t.ApproveListing(ctx, created.ID, now); err != nil {
				return err
			}
			closesAt := now.Add(ttl)
			if _, _, err := t.ActivateListing(ctx, created.ID, closesAt); err != nil {
				return err
			}
			if _, err := t.ScheduleAutoClose(ctx, created.ID, closesAt); err != nil {
				return err
			}
			seeded = true
		}
		return nil
	})
	return seeded, err
}
