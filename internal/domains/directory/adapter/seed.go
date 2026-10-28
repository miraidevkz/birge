package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/directory/model"
	directorystore "birge/internal/domains/directory/store"
	"birge/internal/platform/db"
)

// DemoSpecialist is one DEMO specialist card for a fresh deployment.
// IdempotencyKey must be stable per card (wiring derives it from the preset
// slug), so two concurrent seeds resolve to one row per card through the
// (added_by, idempotency_key) unique index.
type DemoSpecialist struct {
	IdempotencyKey uuid.UUID
	Name           string
	Profession     string
	DistrictID     uuid.UUID
	Needs          []string
	Contacts       string
}

// SeedDemoIfEmpty inserts the DEMO specialists as active cards, added by
// addedBy, when directory.specialists is empty. It never overwrites and
// publishes no events: DEMO cards skip moderation on purpose, so the
// moderation queue does not fill with items nobody submitted. seeded is true
// only when this call inserted at least one card.
func SeedDemoIfEmpty(ctx context.Context, pool *pgxpool.Pool, addedBy uuid.UUID, cards []DemoSpecialist, now time.Time) (seeded bool, err error) {
	if len(cards) == 0 {
		return false, nil
	}
	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		empty, err := directorystore.New(tx).DirectorySpecialistsEmpty(ctx)
		if err != nil {
			return fmt.Errorf("directory: empty check: %w", err)
		}
		if !empty {
			return nil
		}
		t := Bind(tx)
		for i, c := range cards {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("directory: specialist id: %w", err)
			}
			contacts := c.Contacts
			needs := c.Needs
			if needs == nil {
				needs = []string{}
			}
			// Distinct created_at keeps the search order (newest first) stable
			// and equal to the preset order.
			created, inserted, err := t.InsertSpecialist(ctx, model.Specialist{
				ID: id, AddedBy: addedBy, IdempotencyKey: c.IdempotencyKey, Name: c.Name,
				Profession: c.Profession, DistrictID: c.DistrictID, Needs: needs, Contacts: &contacts,
				Status: model.StatusPending, CreatedAt: now.Add(-time.Duration(i) * time.Minute),
			})
			if err != nil {
				return err
			}
			if !inserted {
				continue
			}
			if _, _, err := t.ApproveSpecialist(ctx, created.ID); err != nil {
				return err
			}
			seeded = true
		}
		return nil
	})
	return seeded, err
}
