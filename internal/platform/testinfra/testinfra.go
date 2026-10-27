// Package testinfra starts throwaway PostgreSQL and NATS instances for
// integration tests, without containers: PostgreSQL runs from the local
// binaries in a temporary directory (pgtemp), NATS runs in-process.
//
// Tests skip when PostgreSQL binaries are missing, unless
// BIRGE_REQUIRE_INTEGRATION=1 (set in CI), which makes them fail instead.
package testinfra

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	natsserver "github.com/nats-io/nats-server/v2/server"

	"birge/internal/platform/pgtemp"
)

// Postgres starts an empty PostgreSQL cluster with db/schema/*.sql applied and
// returns a pool connected to it. Everything is removed when the test ends.
func Postgres(t testing.TB) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	inst, err := pgtemp.Start(ctx)
	if errors.Is(err, pgtemp.ErrNoBinaries) {
		missing(t, err.Error())
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inst.Stop)
	pool, err := pgxpool.New(ctx, inst.URL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "db", "schema", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files { // Glob returns names sorted
		schema, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(schema)); err != nil {
			t.Fatalf("apply %s: %v", filepath.Base(f), err)
		}
	}
	return pool
}

// NATS starts an in-process NATS server with JetStream and returns its URL.
func NATS(t testing.TB) string {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1, // random free port
		JetStream: true,
		StoreDir:  t.TempDir(),
		NoLog:     true,
		NoSigs:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(srv.Shutdown)
	return srv.ClientURL()
}

func missing(t testing.TB, why string) {
	t.Helper()
	if os.Getenv("BIRGE_REQUIRE_INTEGRATION") == "1" {
		t.Fatal(why)
	}
	t.Skip(why)
}

func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
