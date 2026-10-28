package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	directoryadapter "birge/internal/domains/directory/adapter"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipmodel "birge/internal/domains/membership/model"
	membershipport "birge/internal/domains/membership/port"
	sharingadapter "birge/internal/domains/sharing/adapter"
	platformdb "birge/internal/platform/db"
	"birge/presets"
)

// demoAuthorID is the fixed author of every DEMO specialist card and listing.
// No member has this id: added_by and author_id are plain ids without a
// foreign key, search and the feed never resolve them, and creating a real
// member would need an identity and a sealed name (a KEK), which migrate does
// not have. A fixed id keeps the seed rows recognisable and makes the
// (author, idempotency_key) indexes deduplicate concurrent seeds.
var demoAuthorID = uuid.MustParse("00000000-0000-4000-8000-0000000de300")

// demoSeedNamespace derives stable idempotency keys from preset slugs.
var demoSeedNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("birge:demo-seed"))

// demoListingTTL is how long DEMO listings stay active. Longer than the real
// listing lifetime (30 days) so a demo stand keeps a feed for a year; the seed
// does not run again once the table has rows, closed ones included.
const demoListingTTL = 365 * 24 * time.Hour

// demoDistricts returns district code -> id for the DEMO reference preset when
// the database holds it (every DEMO city with all its DEMO districts, and
// every DEMO need). ok is false when the deployment uses its own reference
// data: then no DEMO content is seeded, because it would describe districts
// and needs the deployment does not have.
func demoDistricts(ctx context.Context, pool *membershipadapter.Postgres) (districts map[string]uuid.UUID, ok bool, err error) {
	ref, err := presets.LoadReference()
	if err != nil {
		return nil, false, err
	}
	var cities []membershipmodel.City
	var needs []string
	if err := pool.InTx(ctx, func(tx membershipport.Tx) error {
		var err error
		if cities, err = tx.Cities(ctx); err != nil {
			return err
		}
		needs, err = tx.Needs(ctx)
		return err
	}); err != nil {
		return nil, false, err
	}
	byCity := map[string]map[string]uuid.UUID{}
	for _, c := range cities {
		ds := map[string]uuid.UUID{}
		for _, d := range c.Districts {
			ds[d.Code] = d.ID
		}
		byCity[c.Code] = ds
	}
	districts = map[string]uuid.UUID{}
	for _, c := range ref.Cities {
		ds, found := byCity[c.Code]
		if !found {
			return nil, false, nil
		}
		for _, code := range c.Districts {
			id, found := ds[code]
			if !found {
				return nil, false, nil
			}
			districts[code] = id
		}
	}
	known := map[string]bool{}
	for _, n := range needs {
		known[n] = true
	}
	for _, n := range ref.Needs {
		if !known[n] {
			return nil, false, nil
		}
	}
	return districts, true, nil
}

// seedDemoDirectory inserts the DEMO specialist cards (presets/demo/
// directory.json) as active when the DEMO reference is in use and
// directory.specialists is empty. Idempotent: a second migrate is a no-op.
func seedDemoDirectory(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	dir, err := presets.LoadDirectory()
	if err != nil {
		return fmt.Errorf("migrate: demo directory: %w", err)
	}
	pool, err := platformdb.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: demo directory: %w", err)
	}
	defer pool.Close()
	districts, ok, err := demoDistricts(ctx, &membershipadapter.Postgres{Pool: pool})
	if err != nil {
		return fmt.Errorf("migrate: demo directory: %w", err)
	}
	if !ok {
		return nil
	}
	cards := make([]directoryadapter.DemoSpecialist, 0, len(dir.Specialists))
	for _, s := range dir.Specialists {
		id, found := districts[s.District]
		if !found {
			return fmt.Errorf("migrate: demo directory: specialist %s: unknown district %s", s.Slug, s.District)
		}
		cards = append(cards, directoryadapter.DemoSpecialist{
			IdempotencyKey: uuid.NewSHA1(demoSeedNamespace, []byte("directory/"+s.Slug)),
			Name:           s.Name, Profession: s.Profession, DistrictID: id,
			Needs: s.Needs, Contacts: s.Contacts,
		})
	}
	seeded, err := directoryadapter.SeedDemoIfEmpty(ctx, pool, demoAuthorID, cards, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("migrate: demo directory: %w", err)
	}
	if seeded {
		LogDemoPresets(logger, []string{presets.NameDirectory})
	}
	return nil
}

// seedDemoSharing inserts the DEMO listings (presets/demo/listings.json) as
// active when the DEMO reference is in use and sharing.listings is empty.
// Idempotent: a second migrate is a no-op.
func seedDemoSharing(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	ls, err := presets.LoadListings()
	if err != nil {
		return fmt.Errorf("migrate: demo listings: %w", err)
	}
	pool, err := platformdb.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: demo listings: %w", err)
	}
	defer pool.Close()
	districts, ok, err := demoDistricts(ctx, &membershipadapter.Postgres{Pool: pool})
	if err != nil {
		return fmt.Errorf("migrate: demo listings: %w", err)
	}
	if !ok {
		return nil
	}
	listings := make([]sharingadapter.DemoListing, 0, len(ls.Listings))
	for _, l := range ls.Listings {
		id, found := districts[l.District]
		if !found {
			return fmt.Errorf("migrate: demo listings: listing %s: unknown district %s", l.Slug, l.District)
		}
		listings = append(listings, sharingadapter.DemoListing{
			IdempotencyKey: uuid.NewSHA1(demoSeedNamespace, []byte("sharing/"+l.Slug)),
			Type:           l.Type, Category: l.Category, Title: l.Title,
			Description: l.Description, DistrictID: id,
		})
	}
	seeded, err := sharingadapter.SeedDemoIfEmpty(ctx, pool, demoAuthorID, listings, demoListingTTL, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("migrate: demo listings: %w", err)
	}
	if seeded {
		LogDemoPresets(logger, []string{presets.NameListings})
	}
	return nil
}
