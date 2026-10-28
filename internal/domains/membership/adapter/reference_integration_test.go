package adapter_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/adapter"
	"birge/internal/domains/membership/model"
)

func kzSeed(districts ...string) model.ReferenceSeed {
	return model.ReferenceSeed{
		Cities: []model.ReferenceCitySeed{
			{Code: "almaty", Districts: districts},
			{Code: "astana", Districts: []string{"astana_nura"}},
		},
		Needs: []string{"autism", "hearing"},
	}
}

// TestSyncReference covers the reference sync against PostgreSQL: rows that
// existed before it (the demo stand) keep their IDs, new rows get the stable
// IDs, a reseed is a no-op, a removed entry is deactivated and hidden from
// new choices while the member who has it keeps it, and a returning entry is
// reactivated with its old ID.
func TestSyncReference(t *testing.T) {
	pool, svc := setup(t)
	ctx := context.Background()
	store := &adapter.Postgres{Pool: pool}
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)

	// A stand row created by the old seed, with a database-assigned ID.
	var standCity, standDistrict uuid.UUID
	if err := pool.QueryRow(ctx, `WITH c AS (INSERT INTO membership.cities (code) VALUES ('almaty') RETURNING id),
		d AS (INSERT INTO membership.districts (city_id, code) SELECT id, 'almaty_medeu' FROM c RETURNING id)
		SELECT (SELECT id FROM c), (SELECT id FROM d)`).Scan(&standCity, &standDistrict); err != nil {
		t.Fatal(err)
	}

	res, err := store.SyncReference(ctx, kzSeed("almaty_medeu", "almaty_bostandyk"), now)
	if err != nil {
		t.Fatal(err)
	}
	// astana, almaty_bostandyk, astana_nura, autism, hearing.
	if res != (model.ReferenceSyncResult{Inserted: 5}) {
		t.Fatalf("first sync = %+v", res)
	}
	var almaty, medeu, bostandyk uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT id FROM membership.cities WHERE code = 'almaty'),
		(SELECT id FROM membership.districts WHERE code = 'almaty_medeu'),
		(SELECT id FROM membership.districts WHERE code = 'almaty_bostandyk')`).Scan(&almaty, &medeu, &bostandyk); err != nil {
		t.Fatal(err)
	}
	if almaty != standCity || medeu != standDistrict {
		t.Errorf("stand rows changed their IDs: %v %v", almaty, medeu)
	}
	if bostandyk != model.DistrictID("almaty", "almaty_bostandyk") {
		t.Errorf("new district id = %v, want the stable one", bostandyk)
	}

	res, err = store.SyncReference(ctx, kzSeed("almaty_medeu", "almaty_bostandyk"), now)
	if err != nil || res.Changed() {
		t.Fatalf("reseed = %+v, %v; want no change", res, err)
	}

	// A member chooses Medeu, then Medeu leaves the file.
	me := as(uuid.New())
	if _, err := svc.UpdateMe(me, model.ProfileUpdate{DistrictID: &medeu}); err != nil {
		t.Fatal(err)
	}
	res, err = store.SyncReference(ctx, kzSeed("almaty_bostandyk"), now)
	if err != nil || res != (model.ReferenceSyncResult{Deactivated: 1}) {
		t.Fatalf("sync without medeu = %+v, %v", res, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM membership.districts WHERE id = $1 AND NOT active`, medeu); n != 1 {
		t.Fatalf("medeu rows = %d, want one inactive row (never deleted)", n)
	}
	cities, err := svc.Cities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cities {
		for _, d := range c.Districts {
			if d.ID == medeu {
				t.Error("deactivated district still offered")
			}
		}
	}
	m, err := svc.UpdateMe(me, model.ProfileUpdate{DistrictID: &medeu, Lang: ptrLang(model.LangKK)})
	if err != nil || m.DistrictID == nil || *m.DistrictID != medeu {
		t.Fatalf("member keeping medeu = %+v, %v", m.DistrictID, err)
	}
	other := as(uuid.New())
	if _, err := svc.UpdateMe(other, model.ProfileUpdate{DistrictID: &medeu}); !errors.Is(err, model.ErrUnknownDistrict) {
		t.Fatalf("new choice of a deactivated district: %v", err)
	}

	// Medeu returns: same row, active again.
	res, err = store.SyncReference(ctx, kzSeed("almaty_medeu", "almaty_bostandyk"), now)
	if err != nil || res != (model.ReferenceSyncResult{Reactivated: 1}) {
		t.Fatalf("sync with medeu back = %+v, %v", res, err)
	}
	if _, err := svc.UpdateMe(other, model.ProfileUpdate{DistrictID: &medeu}); err != nil {
		t.Fatalf("reactivated district refused: %v", err)
	}

	// Dropping a city hides it and its districts; needs work the same way.
	res, err = store.SyncReference(ctx, model.ReferenceSeed{
		Cities: []model.ReferenceCitySeed{{Code: "almaty", Districts: []string{"almaty_medeu", "almaty_bostandyk"}}},
		Needs:  []string{"autism"},
	}, now)
	if err != nil || res != (model.ReferenceSyncResult{Deactivated: 3}) {
		t.Fatalf("sync without astana and hearing = %+v, %v", res, err)
	}
	if cities, err := svc.Cities(ctx); err != nil || len(cities) != 1 || cities[0].Code != "almaty" {
		t.Fatalf("cities = %+v, %v", cities, err)
	}
	if needs, err := svc.Needs(ctx); err != nil || len(needs) != 1 || needs[0] != "autism" {
		t.Fatalf("needs = %v, %v", needs, err)
	}
}

// TestSyncReferenceConcurrent checks that concurrent migrate jobs serialize on
// the advisory lock: all succeed and the rows exist once.
func TestSyncReferenceConcurrent(t *testing.T) {
	pool, _ := setup(t)
	store := &adapter.Postgres{Pool: pool}
	const jobs = 5
	errs := make(chan error, jobs)
	var wg sync.WaitGroup
	for range jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.SyncReference(context.Background(), kzSeed("almaty_medeu"), time.Now())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM membership.districts`); n != 2 {
		t.Fatalf("districts = %d, want 2", n)
	}
}

func TestSyncReferenceRejectsBadSeed(t *testing.T) {
	pool, _ := setup(t)
	store := &adapter.Postgres{Pool: pool}
	tests := []struct {
		name string
		seed model.ReferenceSeed
	}{
		{"empty", model.ReferenceSeed{}},
		{"bad code", model.ReferenceSeed{Cities: []model.ReferenceCitySeed{{Code: "Almaty", Districts: []string{"a_b"}}}, Needs: []string{"autism"}}},
		{"district in two cities", model.ReferenceSeed{Cities: []model.ReferenceCitySeed{
			{Code: "city_a", Districts: []string{"same"}}, {Code: "city_b", Districts: []string{"same"}},
		}, Needs: []string{"autism"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.SyncReference(context.Background(), tt.seed, time.Now()); !errors.Is(err, model.ErrInvalidReference) {
				t.Fatalf("error = %v", err)
			}
			if n := count(t, pool, `SELECT count(*) FROM membership.cities`); n != 0 {
				t.Fatalf("cities = %d after a refused seed", n)
			}
		})
	}
}

func ptrLang(l model.Lang) *model.Lang { return &l }
