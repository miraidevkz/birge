// Package pgtemp starts a throwaway PostgreSQL cluster from the local
// binaries (initdb, postgres) in a temporary directory, listening only on
// 127.0.0.1 with a random port. Used by tests and by the migration tool.
package pgtemp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoBinaries means initdb or postgres is not in PATH.
var ErrNoBinaries = errors.New("pgtemp: initdb/postgres not found in PATH")

// Instance is a running cluster. User "birge", database "postgres", trust auth.
type Instance struct {
	Dir  string
	Port int
	cmd  *exec.Cmd
	logf *os.File
}

// URL is a libpq/pgx connection string for the cluster.
func (i *Instance) URL() string {
	return fmt.Sprintf("host=127.0.0.1 port=%d user=birge dbname=postgres sslmode=disable", i.Port)
}

// Log returns the server log, for error messages.
func (i *Instance) Log() string {
	b, _ := os.ReadFile(filepath.Join(i.Dir, "postgres.log"))
	return string(b)
}

// Start creates and starts a cluster and waits until it accepts connections.
func Start(ctx context.Context) (*Instance, error) {
	initdb, err1 := exec.LookPath("initdb")
	postgres, err2 := exec.LookPath("postgres")
	if err1 != nil || err2 != nil {
		return nil, ErrNoBinaries
	}
	dir, err := os.MkdirTemp("", "birge-pg-")
	if err != nil {
		return nil, fmt.Errorf("pgtemp: %w", err)
	}
	inst := &Instance{Dir: dir}
	fail := func(err error) (*Instance, error) {
		inst.Stop()
		return nil, err
	}
	data := filepath.Join(dir, "data")
	if out, err := exec.CommandContext(ctx, initdb, "-D", data, "-U", "birge", "--auth=trust", "-E", "UTF8", "--no-sync", "--no-instructions").CombinedOutput(); err != nil {
		return fail(fmt.Errorf("pgtemp: initdb: %w\n%s", err, out))
	}
	if inst.Port, err = freePort(); err != nil {
		return fail(err)
	}
	if inst.logf, err = os.Create(filepath.Join(dir, "postgres.log")); err != nil {
		return fail(fmt.Errorf("pgtemp: %w", err))
	}
	inst.cmd = exec.Command(postgres, "-D", data, "-k", dir, //nolint:noctx // lives until Stop, not tied to ctx
		"-h", "127.0.0.1", "-p", strconv.Itoa(inst.Port),
		"-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off")
	inst.cmd.Stdout, inst.cmd.Stderr = inst.logf, inst.logf
	if err := inst.cmd.Start(); err != nil {
		return fail(fmt.Errorf("pgtemp: start postgres: %w", err))
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := inst.wait(wctx); err != nil {
		return fail(fmt.Errorf("pgtemp: postgres did not start: %w\n%s", err, inst.Log()))
	}
	return inst, nil
}

// Stop shuts the cluster down and removes its directory.
func (i *Instance) Stop() {
	if i.cmd != nil && i.cmd.Process != nil {
		_ = i.cmd.Process.Signal(os.Interrupt) // fast shutdown
		_ = i.cmd.Wait()
	}
	if i.logf != nil {
		_ = i.logf.Close()
	}
	_ = os.RemoveAll(i.Dir)
}

func (i *Instance) wait(ctx context.Context) error {
	var last error
	for {
		pool, err := pgxpool.New(ctx, i.URL())
		if err == nil {
			err = pool.Ping(ctx)
			pool.Close()
			if err == nil {
				return nil
			}
		}
		last = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (last error: %w)", ctx.Err(), last)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("pgtemp: free port: %w", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}
