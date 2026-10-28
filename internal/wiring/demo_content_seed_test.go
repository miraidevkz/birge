package wiring

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	directoryadapter "birge/internal/domains/directory/adapter"
	directorymodel "birge/internal/domains/directory/model"
	directoryport "birge/internal/domains/directory/port"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipmodel "birge/internal/domains/membership/model"
	sharingadapter "birge/internal/domains/sharing/adapter"
	sharingmodel "birge/internal/domains/sharing/model"
	sharingport "birge/internal/domains/sharing/port"
	platformdb "birge/internal/platform/db"
	"birge/internal/platform/testinfra"
	"birge/presets"
)

// TestDemoContentSeedOnce checks that a first migrate seeds the DEMO
// specialists and listings into the states search and the feed return, and a
// second migrate changes nothing and logs nothing.
func TestDemoContentSeedOnce(t *testing.T) {
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
	for _, name := range []string{presets.NameDirectory, presets.NameListings} {
		if !strings.Contains(buf.String(), `"event":"DemoPreset","name":"`+name+`"`) {
			t.Fatalf("first migrate missing DemoPreset %s log: %s", name, buf.String())
		}
	}

	pool, err := platformdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	dir, err := presets.LoadDirectory()
	if err != nil {
		t.Fatal(err)
	}
	ls, err := presets.LoadListings()
	if err != nil {
		t.Fatal(err)
	}

	// Search and the feed return every DEMO row, for any viewer.
	viewer := uuid.New()
	var found []directorymodel.Specialist
	if err := (&directoryadapter.PostgresStore{Pool: pool}).InTx(ctx, func(tx directoryport.Tx) error {
		var err error
		found, err = tx.SearchActiveSpecialists(ctx, directorymodel.SearchFilter{Limit: 50})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(found) != len(dir.Specialists) {
		t.Fatalf("active specialists = %d, want %d", len(found), len(dir.Specialists))
	}
	if found[0].Name != dir.Specialists[0].Name {
		t.Fatalf("newest specialist = %q, want preset order (%q first)", found[0].Name, dir.Specialists[0].Name)
	}
	for _, s := range found {
		if s.AddedBy != demoAuthorID || !strings.Contains(s.Name, "DEMO") {
			t.Fatalf("specialist %s: added_by=%s name=%q", s.ID, s.AddedBy, s.Name)
		}
	}
	var feed []sharingmodel.Listing
	if err := (&sharingadapter.PostgresStore{Pool: pool}).InTx(ctx, func(tx sharingport.Tx) error {
		var err error
		feed, err = tx.ListActiveListings(ctx, sharingmodel.FeedFilter{ViewerID: viewer, Limit: 50})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(feed) != len(ls.Listings) {
		t.Fatalf("active listings = %d, want %d", len(feed), len(ls.Listings))
	}
	for _, l := range feed {
		if l.AuthorID != demoAuthorID || l.ClosesAt == nil || l.ApprovedAt == nil || l.PhotoUploadID != nil {
			t.Fatalf("listing %s: %+v", l.ID, l)
		}
	}
	var timers, outbox int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM platform.timers WHERE kind = $1 AND done_at IS NULL),
		(SELECT count(*) FROM platform.outbox)`, sharingmodel.TimerAutoClose).Scan(&timers, &outbox); err != nil {
		t.Fatal(err)
	}
	if timers != len(ls.Listings) {
		t.Fatalf("auto-close timers = %d, want %d", timers, len(ls.Listings))
	}
	if outbox != 0 {
		t.Fatalf("DEMO seed must publish nothing, outbox = %d", outbox)
	}

	buf.Reset()
	if err := Migrate(ctx, logger, cfg); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{presets.NameDirectory, presets.NameListings} {
		if strings.Contains(buf.String(), `"name":"`+name+`"`) {
			t.Fatalf("second migrate must not re-seed or re-log %s: %s", name, buf.String())
		}
	}
	var specialists, listings, timers2 int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM directory.specialists),
		(SELECT count(*) FROM sharing.listings),
		(SELECT count(*) FROM platform.timers WHERE kind = $1)`, sharingmodel.TimerAutoClose).Scan(&specialists, &listings, &timers2); err != nil {
		t.Fatal(err)
	}
	if specialists != len(dir.Specialists) || listings != len(ls.Listings) || timers2 != len(ls.Listings) {
		t.Fatalf("after second migrate: specialists=%d listings=%d timers=%d", specialists, listings, timers2)
	}
}

// TestDemoContentSkippedWithOwnReference checks that a deployment with its
// own reference data (no DEMO city or districts) gets no DEMO specialists or
// listings, and nothing is logged.
func TestDemoContentSkippedWithOwnReference(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	res, err := (&membershipadapter.Postgres{Pool: pool}).SyncReference(ctx, membershipmodel.ReferenceSeed{
		Cities: []membershipmodel.ReferenceCitySeed{{Code: "own_city", Districts: []string{"own_district"}}},
		Needs:  []string{"autism", "hearing"},
	}, time.Now())
	if err != nil || res.Inserted != 4 {
		t.Fatalf("own reference: %+v err=%v", res, err)
	}
	cc := pool.Config().ConnConfig
	url := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable", cc.Host, cc.Port, cc.User, cc.Database)

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	if err := seedDemoDirectory(ctx, logger, url); err != nil {
		t.Fatal(err)
	}
	if err := seedDemoSharing(ctx, logger, url); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("no DEMO content expected, got logs: %s", buf.String())
	}
	var specialists, listings int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM directory.specialists), (SELECT count(*) FROM sharing.listings)`).Scan(&specialists, &listings); err != nil {
		t.Fatal(err)
	}
	if specialists != 0 || listings != 0 {
		t.Fatalf("specialists=%d listings=%d, want none", specialists, listings)
	}
}

// TestDemoContentNeverJoinsExistingRows checks that the seed adds nothing to
// a directory or feed that already has rows, even with the DEMO reference.
func TestDemoContentNeverJoinsExistingRows(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	seed, err := referenceSeed([]string{presets.ReferenceDemo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&membershipadapter.Postgres{Pool: pool}).SyncReference(ctx, seed, time.Now()); err != nil {
		t.Fatal(err)
	}
	var district uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM membership.districts ORDER BY code LIMIT 1`).Scan(&district); err != nil {
		t.Fatal(err)
	}
	member := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO directory.specialists (id, added_by, idempotency_key, name, profession, district_id, status, created_at)
		VALUES ($1, $2, $3, 'Real card', 'psychologist', $4, 'pending', now())`, uuid.New(), member, uuid.New(), district); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO sharing.listings (id, author_id, idempotency_key, type, category, title, description, district_id, status, created_at)
		VALUES ($1, $2, $3, 'give', 'books', 'Real listing', 'Real text', $4, 'pending', now())`, uuid.New(), member, uuid.New(), district); err != nil {
		t.Fatal(err)
	}
	cc := pool.Config().ConnConfig
	url := fmt.Sprintf("host=%s port=%d user=%s dbname=%s sslmode=disable", cc.Host, cc.Port, cc.User, cc.Database)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	if err := seedDemoDirectory(ctx, logger, url); err != nil {
		t.Fatal(err)
	}
	if err := seedDemoSharing(ctx, logger, url); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("no DEMO content expected, got logs: %s", buf.String())
	}
	var specialists, listings int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM directory.specialists), (SELECT count(*) FROM sharing.listings)`).Scan(&specialists, &listings); err != nil {
		t.Fatal(err)
	}
	if specialists != 1 || listings != 1 {
		t.Fatalf("specialists=%d listings=%d, want the one existing row each", specialists, listings)
	}
}
