// Package testinfra starts throwaway PostgreSQL and NATS instances for
// integration tests, without containers: PostgreSQL runs from the local
// binaries in a temporary directory on a unix socket, NATS runs in-process.
//
// Tests skip when PostgreSQL binaries are missing, unless
// BIRGE_REQUIRE_INTEGRATION=1 (set in CI), which makes them fail instead.
package testinfra

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

// Postgres starts an empty PostgreSQL cluster with db/schema.sql applied and
// returns a pool connected to it. Everything is removed when the test ends.
func Postgres(t testing.TB) *pgxpool.Pool {
	t.Helper()
	initdb, err := exec.LookPath("initdb")
	if err != nil {
		missing(t, "initdb not found in PATH")
	}
	postgres, err := exec.LookPath("postgres")
	if err != nil {
		missing(t, "postgres not found in PATH")
	}

	dir, err := os.MkdirTemp("", "birge-pg-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	data := filepath.Join(dir, "data")

	out, err := exec.Command(initdb, "-D", data, "-U", "birge", "--auth=trust", "-E", "UTF8", "--no-sync", "--no-instructions").CombinedOutput()
	if err != nil {
		t.Fatalf("initdb: %v\n%s", err, out)
	}

	cmd := exec.Command(postgres, "-D", data, "-k", dir, "-c", "listen_addresses=",
		"-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off")
	logf, err := os.Create(filepath.Join(dir, "postgres.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start postgres: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt) // fast shutdown
		_ = cmd.Wait()
		_ = logf.Close()
	})

	url := fmt.Sprintf("host=%s user=birge dbname=postgres sslmode=disable", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := waitForPostgres(ctx, url)
	if err != nil {
		log, _ := os.ReadFile(filepath.Join(dir, "postgres.log"))
		t.Fatalf("postgres did not start: %v\n%s", err, log)
	}
	t.Cleanup(pool.Close)

	schema, err := os.ReadFile(filepath.Join(repoRoot(t), "db", "schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(schema)); err != nil {
		t.Fatalf("apply schema: %v", err)
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

func waitForPostgres(ctx context.Context, url string) (*pgxpool.Pool, error) {
	for {
		pool, err := pgxpool.New(ctx, url)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool, nil
			}
			pool.Close()
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (last error: %w)", ctx.Err(), err)
		case <-time.After(50 * time.Millisecond):
		}
	}
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
