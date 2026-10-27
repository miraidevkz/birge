package wiring

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	platformdb "birge/internal/platform/db"
	"birge/internal/platform/testinfra"
	"birge/presets"
)

func TestDemoReferenceSeedOnce(t *testing.T) {
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
	var cities, needs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM membership.cities), (SELECT count(*) FROM membership.needs)`).Scan(&cities, &needs); err != nil {
		t.Fatal(err)
	}
	ref, err := presets.LoadReference()
	if err != nil {
		t.Fatal(err)
	}
	if cities != len(ref.Cities) || needs != len(ref.Needs) {
		t.Fatalf("cities=%d needs=%d, want %d and %d", cities, needs, len(ref.Cities), len(ref.Needs))
	}
	var districtCode string
	if err := pool.QueryRow(ctx, `SELECT code FROM membership.districts ORDER BY code LIMIT 1`).Scan(&districtCode); err != nil {
		t.Fatal(err)
	}
	if districtCode != "demo_district_a" {
		t.Fatalf("district = %q", districtCode)
	}

	buf.Reset()
	if err := Migrate(ctx, logger, cfg); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"name":"reference"`) {
		t.Fatalf("second migrate must not re-seed or re-log reference: %s", buf.String())
	}
	var cities2 int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM membership.cities`).Scan(&cities2); err != nil {
		t.Fatal(err)
	}
	if cities2 != cities {
		t.Fatalf("cities after second migrate = %d, want %d", cities2, cities)
	}
}
