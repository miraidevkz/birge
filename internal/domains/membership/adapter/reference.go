package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/store"
	"birge/internal/platform/db"
)

// SyncReference makes the active cities, districts and needs equal to ref,
// in one transaction (birge migrate):
//   - a missing entry is inserted with its stable ID (model.CityID,
//     model.DistrictID), so every deployment and every reseed gives the same
//     code the same ID;
//   - an existing entry keeps its ID, whatever it is (rows created before the
//     sync, such as the DEMO stand's, keep the IDs members, rooms, cards and
//     listings refer to), and is reactivated if it was inactive;
//   - an active entry that ref no longer lists is deactivated, never deleted:
//     members who chose it keep it, new choices no longer see it.
//
// Repeating the call with the same ref changes nothing. Entries are matched by
// code (districts by city and code). An advisory lock serializes concurrent
// migrate jobs.
func (p *Postgres) SyncReference(ctx context.Context, ref model.ReferenceSeed, now time.Time) (model.ReferenceSyncResult, error) {
	var res model.ReferenceSyncResult
	if err := ref.Validate(); err != nil {
		return res, err
	}
	err := db.InTx(ctx, p.Pool, func(tx pgx.Tx) error {
		res = model.ReferenceSyncResult{}
		q := store.New(tx)
		if err := q.LockReference(ctx); err != nil {
			return fmt.Errorf("membership: reference lock: %w", err)
		}
		cityIDs, err := syncCities(ctx, q, ref, now, &res)
		if err != nil {
			return err
		}
		if err := syncDistricts(ctx, q, ref, cityIDs, now, &res); err != nil {
			return err
		}
		return syncNeeds(ctx, q, ref, now, &res)
	})
	if err != nil {
		return model.ReferenceSyncResult{}, err
	}
	return res, nil
}

// syncCities returns city code -> ID for every city of ref.
func syncCities(ctx context.Context, q *store.Queries, ref model.ReferenceSeed, now time.Time, res *model.ReferenceSyncResult) (map[string]uuid.UUID, error) {
	rows, err := q.ListAllCities(ctx)
	if err != nil {
		return nil, fmt.Errorf("membership: list all cities: %w", err)
	}
	existing := make(map[string]store.ListAllCitiesRow, len(rows))
	for _, r := range rows {
		existing[r.Code] = r
	}
	ids := make(map[string]uuid.UUID, len(ref.Cities))
	for _, c := range ref.Cities {
		r, found := existing[c.Code]
		switch {
		case !found:
			id := model.CityID(c.Code)
			if err := q.InsertCity(ctx, store.InsertCityParams{ID: id, Code: c.Code, Now: now}); err != nil {
				return nil, fmt.Errorf("membership: insert city: %w", err)
			}
			ids[c.Code] = id
			res.Inserted++
		case !r.Active:
			if err := q.SetCityActive(ctx, store.SetCityActiveParams{Active: true, Now: now, ID: r.ID}); err != nil {
				return nil, fmt.Errorf("membership: reactivate city: %w", err)
			}
			ids[c.Code] = r.ID
			res.Reactivated++
		default:
			ids[c.Code] = r.ID
		}
	}
	for _, r := range rows {
		if _, keep := ids[r.Code]; keep || !r.Active {
			continue
		}
		if err := q.SetCityActive(ctx, store.SetCityActiveParams{Active: false, Now: now, ID: r.ID}); err != nil {
			return nil, fmt.Errorf("membership: deactivate city: %w", err)
		}
		res.Deactivated++
	}
	return ids, nil
}

func syncDistricts(ctx context.Context, q *store.Queries, ref model.ReferenceSeed, cityIDs map[string]uuid.UUID, now time.Time, res *model.ReferenceSyncResult) error {
	type key struct {
		city uuid.UUID
		code string
	}
	rows, err := q.ListAllDistricts(ctx)
	if err != nil {
		return fmt.Errorf("membership: list all districts: %w", err)
	}
	existing := make(map[key]store.ListAllDistrictsRow, len(rows))
	for _, r := range rows {
		existing[key{r.CityID, r.Code}] = r
	}
	wanted := map[key]bool{}
	for _, c := range ref.Cities {
		cityID := cityIDs[c.Code]
		for _, code := range c.Districts {
			k := key{cityID, code}
			wanted[k] = true
			r, found := existing[k]
			switch {
			case !found:
				params := store.InsertDistrictParams{ID: model.DistrictID(c.Code, code), CityID: cityID, Code: code, Now: now}
				if err := q.InsertDistrict(ctx, params); err != nil {
					return fmt.Errorf("membership: insert district: %w", err)
				}
				res.Inserted++
			case !r.Active:
				if err := q.SetDistrictActive(ctx, store.SetDistrictActiveParams{Active: true, Now: now, ID: r.ID}); err != nil {
					return fmt.Errorf("membership: reactivate district: %w", err)
				}
				res.Reactivated++
			}
		}
	}
	for _, r := range rows {
		if wanted[key{r.CityID, r.Code}] || !r.Active {
			continue
		}
		if err := q.SetDistrictActive(ctx, store.SetDistrictActiveParams{Active: false, Now: now, ID: r.ID}); err != nil {
			return fmt.Errorf("membership: deactivate district: %w", err)
		}
		res.Deactivated++
	}
	return nil
}

func syncNeeds(ctx context.Context, q *store.Queries, ref model.ReferenceSeed, now time.Time, res *model.ReferenceSyncResult) error {
	rows, err := q.ListAllNeeds(ctx)
	if err != nil {
		return fmt.Errorf("membership: list all needs: %w", err)
	}
	existing := make(map[string]bool, len(rows)) // code -> active
	for _, r := range rows {
		existing[r.Code] = r.Active
	}
	wanted := make(map[string]bool, len(ref.Needs))
	for _, code := range ref.Needs {
		wanted[code] = true
		active, found := existing[code]
		switch {
		case !found:
			if err := q.InsertNeed(ctx, store.InsertNeedParams{Code: code, Now: now}); err != nil {
				return fmt.Errorf("membership: insert need: %w", err)
			}
			res.Inserted++
		case !active:
			if err := q.SetNeedActive(ctx, store.SetNeedActiveParams{Active: true, Now: now, Code: code}); err != nil {
				return fmt.Errorf("membership: reactivate need: %w", err)
			}
			res.Reactivated++
		}
	}
	for _, r := range rows {
		if wanted[r.Code] || !r.Active {
			continue
		}
		if err := q.SetNeedActive(ctx, store.SetNeedActiveParams{Active: false, Now: now, Code: r.Code}); err != nil {
			return fmt.Errorf("membership: deactivate need: %w", err)
		}
		res.Deactivated++
	}
	return nil
}
