package wiring

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	membershipadapter "birge/internal/domains/membership/adapter"
	membershipmodel "birge/internal/domains/membership/model"
	membershipport "birge/internal/domains/membership/port"
	platformdb "birge/internal/platform/db"
	"birge/internal/platform/testinfra"
	"birge/presets"
)

// referenceIDs returns "city/<code>" and "district/<code>" -> "<id>:<active>",
// and "need/<code>" -> "<active>".
func referenceIDs(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT 'city/' || code, id::text || ':' || active::text FROM membership.cities
		UNION ALL
		SELECT 'district/' || code, id::text || ':' || active::text FROM membership.districts
		UNION ALL
		SELECT 'need/' || code, active::text FROM membership.needs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// groupRoomIDs returns "district/<district id>" and "topic/<code>" -> room id
// for every chat group room.
func groupRoomIDs(t *testing.T, pool *pgxpool.Pool) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT CASE kind WHEN 'district' THEN 'district/' || district_id::text ELSE 'topic/' || topic_code END, id::text
		FROM chat.rooms WHERE kind IN ('district', 'topic')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func selectableCities(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	var cities []membershipmodel.City
	if err := (&membershipadapter.Postgres{Pool: pool}).InTx(context.Background(), func(tx membershipport.Tx) error {
		var err error
		cities, err = tx.Cities(context.Background())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range cities {
		out[c.Code] = true
	}
	return out
}

// TestReferenceSyncOnMigrate checks the reference sync of birge migrate: the
// default sets (DEMO and Kazakhstan) are seeded once with stable IDs; a
// second migrate changes nothing; dropping a set deactivates its rows without
// deleting them, the member who chose one keeps it, and bringing the set back
// reactivates the same rows.
func TestReferenceSyncOnMigrate(t *testing.T) {
	ctx := context.Background()
	admin := testinfra.Postgres(t)
	for _, stmt := range []string{"CREATE DATABASE birge_seed", "CREATE ROLE birge_app NOLOGIN"} {
		if _, err := admin.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	cc := admin.Config().ConnConfig
	url := fmt.Sprintf("host=%s port=%d user=%s dbname=birge_seed sslmode=disable", cc.Host, cc.Port, cc.User)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	cfg := Config{DatabaseURL: url, ApplyRoles: true}

	if err := Migrate(ctx, logger, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"event":"DemoPreset"`) || !strings.Contains(buf.String(), `"name":"reference"`) {
		t.Fatalf("first migrate missing DemoPreset reference log: %s", buf.String())
	}

	pool, err := platformdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	want, err := referenceSeed(presets.DefaultReferenceSets)
	if err != nil {
		t.Fatal(err)
	}
	wantDistricts := 0
	for _, c := range want.Cities {
		wantDistricts += len(c.Districts)
	}
	first := referenceIDs(t, pool)
	if len(first) != len(want.Cities)+wantDistricts+len(want.Needs) {
		t.Fatalf("reference rows = %d, want %d cities, %d districts, %d needs", len(first), len(want.Cities), wantDistricts, len(want.Needs))
	}
	for k, v := range first {
		if !strings.HasSuffix(v, "true") {
			t.Errorf("%s not active after the first migrate: %s", k, v)
		}
	}
	if got, wantID := first["district/almaty_bostandyk"], membershipmodel.DistrictID("almaty", "almaty_bostandyk").String()+":true"; got != wantID {
		t.Errorf("almaty_bostandyk = %s, want the stable %s", got, wantID)
	}
	if got, wantID := first["city/astana"], membershipmodel.CityID("astana").String()+":true"; got != wantID {
		t.Errorf("astana = %s, want the stable %s", got, wantID)
	}
	// A fresh database gets one chat room per active district and needs code.
	var districtRooms, topicRooms int
	if err := pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind = 'district'), count(*) FILTER (WHERE kind = 'topic') FROM chat.rooms`).Scan(&districtRooms, &topicRooms); err != nil {
		t.Fatal(err)
	}
	if districtRooms != wantDistricts || topicRooms != len(want.Needs) {
		t.Errorf("district rooms = %d, topic rooms = %d; want %d, %d", districtRooms, topicRooms, wantDistricts, len(want.Needs))
	}
	if !strings.Contains(buf.String(), fmt.Sprintf(`"districts_created":%d`, wantDistricts)) {
		t.Errorf("first migrate missing the created district rooms count: %s", buf.String())
	}
	var almatyTitle string
	if err := pool.QueryRow(ctx, `SELECT title_key FROM chat.rooms WHERE kind = 'district' AND district_id = $1`,
		membershipmodel.DistrictID("almaty", "almaty_bostandyk")).Scan(&almatyTitle); err != nil || almatyTitle != "chat.room.district.almaty_bostandyk" {
		t.Errorf("almaty_bostandyk room title = %q, %v", almatyTitle, err)
	}

	// An older stand: the DEMO seed ran before the Kazakhstan districts
	// existed, so only the DEMO districts have rooms. The next migrate adds
	// the missing rooms and keeps the DEMO ones with their ids.
	tag, err := pool.Exec(ctx, `DELETE FROM chat.rooms WHERE kind = 'district'
		AND district_id NOT IN (SELECT id FROM membership.districts WHERE code LIKE 'demo\_%')`)
	if err != nil {
		t.Fatal(err)
	}
	missing := int(tag.RowsAffected())
	if missing == 0 || missing == wantDistricts {
		t.Fatalf("older stand setup removed %d of %d district rooms", missing, wantDistricts)
	}
	demoRooms := groupRoomIDs(t, pool)
	buf.Reset()
	if err := Migrate(ctx, logger, cfg); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), fmt.Sprintf(`"districts_created":%d`, missing)) || !strings.Contains(buf.String(), `"topics_created":0`) {
		t.Errorf("older stand migrate created the wrong rooms: %s", buf.String())
	}
	rooms := groupRoomIDs(t, pool)
	if len(rooms) != wantDistricts+len(want.Needs) {
		t.Errorf("group rooms after the older stand migrate = %d, want %d", len(rooms), wantDistricts+len(want.Needs))
	}
	for k, v := range demoRooms {
		if rooms[k] != v {
			t.Errorf("existing room %s changed its id: %s -> %s", k, v, rooms[k])
		}
	}

	// A member on a DEMO district, as on the demo stand.
	var demoDistrict uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM membership.districts WHERE code = 'demo_district_a'`).Scan(&demoDistrict); err != nil {
		t.Fatal(err)
	}
	memberID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO membership.members (id, identity_id, district_id) VALUES ($1, $2, $3)`,
		memberID, uuid.New(), demoDistrict); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	if err := Migrate(ctx, logger, cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"name":"reference"`) {
		t.Fatalf("second migrate must not re-log the reference preset: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"inserted":0,"reactivated":0,"deactivated":0`) {
		t.Fatalf("second migrate changed reference rows: %s", buf.String())
	}
	if again := referenceIDs(t, pool); fmt.Sprint(again) != fmt.Sprint(first) {
		t.Fatal("second migrate changed reference IDs or flags")
	}
	if !strings.Contains(buf.String(), `"districts_created":0`) || !strings.Contains(buf.String(), `"topics_created":0`) {
		t.Errorf("repeated migrate created chat rooms: %s", buf.String())
	}
	if again := groupRoomIDs(t, pool); fmt.Sprint(again) != fmt.Sprint(rooms) {
		t.Error("repeated migrate changed chat room ids")
	}

	// The deployment drops the DEMO set: its rows stay, inactive.
	if err := Migrate(ctx, logger, Config{DatabaseURL: url, ApplyRoles: true, ReferencePresets: []string{presets.ReferenceKZ}}); err != nil {
		t.Fatal(err)
	}
	kzOnly := referenceIDs(t, pool)
	if len(kzOnly) != len(first) {
		t.Fatalf("rows after dropping DEMO = %d, want %d (never deleted)", len(kzOnly), len(first))
	}
	idOf := func(v string) string { return strings.TrimSuffix(strings.TrimSuffix(v, ":true"), ":false") }
	for k, v := range first {
		demo := strings.Contains(k, "demo_")
		if idOf(kzOnly[k]) != idOf(v) {
			t.Errorf("%s changed its ID: %s -> %s", k, v, kzOnly[k])
		}
		if demo != strings.HasSuffix(kzOnly[k], ":false") {
			t.Errorf("%s active flag after dropping DEMO: %s", k, kzOnly[k])
		}
	}
	if cities := selectableCities(t, pool); cities["demo_city"] || !cities["almaty"] {
		t.Errorf("selectable cities after dropping DEMO = %v", cities)
	}
	var kept uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT district_id FROM membership.members WHERE id = $1`, memberID).Scan(&kept); err != nil || kept != demoDistrict {
		t.Fatalf("member district = %v, %v; want the kept %v", kept, err, demoDistrict)
	}
	// The deactivated DEMO districts keep their rooms (history), same ids.
	if after := groupRoomIDs(t, pool); fmt.Sprint(after) != fmt.Sprint(rooms) {
		t.Errorf("chat rooms after deactivation changed: %d rooms, want the same %d", len(after), len(rooms))
	}

	// Bringing the set back reactivates the same rows.
	buf.Reset()
	if err := Migrate(ctx, logger, cfg); err != nil {
		t.Fatal(err)
	}
	if back := referenceIDs(t, pool); fmt.Sprint(back) != fmt.Sprint(first) {
		t.Fatal("restoring the DEMO set did not reactivate the same rows")
	}
	if back := groupRoomIDs(t, pool); fmt.Sprint(back) != fmt.Sprint(rooms) {
		t.Error("restoring the DEMO set changed chat rooms")
	}
	if !strings.Contains(buf.String(), `"name":"reference"`) {
		t.Errorf("reactivating the DEMO set must log DemoPreset: %s", buf.String())
	}
}
