// Command birge-dbtool generates and checks database migrations (ADR-0006).
//
//	birge-dbtool migration <name>  print the migration that turns the migrated
//	                               database into db/schema/*.sql (save it under
//	                               db/migrations/ with the printed file name)
//	birge-dbtool check             apply all migrations to a fresh database,
//	                               verify the result equals db/schema/*.sql and
//	                               run the destructive-operation gate
//
// It needs PostgreSQL binaries (initdb, postgres) and psqldef in PATH, or
// psqldef given with -psqldef.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"

	"birge/internal/platform/pgtemp"
)

const header = "SET lock_timeout = '5s';\nSET statement_timeout = '60s';\n"

func main() {
	psqldef := flag.String("psqldef", "psqldef", "psqldef binary")
	schema := flag.String("schema", "db/schema", "directory with the desired schema files (applied in name order)")
	dir := flag.String("migrations", "db/migrations", "migrations directory")
	flag.Parse()
	t := tool{psqldef: *psqldef, schema: *schema, dir: *dir, out: os.Stdout, now: time.Now}
	var err error
	switch flag.Arg(0) {
	case "migration":
		err = t.migration(context.Background(), flag.Arg(1))
	case "check":
		err = t.check(context.Background())
	default:
		err = errors.New("usage: birge-dbtool [flags] migration <name> | check")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "birge-dbtool:", err)
		os.Exit(1)
	}
}

type tool struct {
	psqldef, schema, dir string
	out                  io.Writer
	now                  func() time.Time
}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(_[a-z0-9]+)*$`)

func (t tool) migration(ctx context.Context, name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("migration name %q must be snake_case", name)
	}
	db, inst, err := t.migrated(ctx)
	if err != nil {
		return err
	}
	defer inst.Stop()
	defer func() { _ = db.Close() }()

	ddl, err := t.diff(ctx, inst)
	if err != nil {
		return err
	}
	if ddl == "" {
		return errors.New("db/schema already matches the migrations; nothing to generate")
	}
	body := "-- +goose Up\n" + header + ddl + "\n"
	if v := gate("generated", body); v != "" {
		return fmt.Errorf("%s\nwrite this migration by hand as expand/contract (see the db-migration skill)", v)
	}
	// The generated migration must bring the database exactly to the desired schema.
	if _, err := db.ExecContext(ctx, header+ddl); err != nil {
		return fmt.Errorf("generated migration does not apply: %w\n%s", err, ddl)
	}
	if rest, err := t.diff(ctx, inst); err != nil || rest != "" {
		return fmt.Errorf("generated migration leaves a difference (%w):\n%s", err, rest)
	}
	file := fmt.Sprintf("%s_%s.sql", t.now().UTC().Format("20060102150405"), name)
	_, err = fmt.Fprintf(t.out, "-- file: db/migrations/%s\n%s", file, body)
	return err
}

func (t tool) check(ctx context.Context) error {
	files, err := os.ReadDir(t.dir)
	if err != nil {
		return err
	}
	var problems []string
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		b, err := os.ReadFile(t.dir + "/" + f.Name())
		if err != nil {
			return err
		}
		if v := gate(f.Name(), string(b)); v != "" {
			problems = append(problems, v)
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	db, inst, err := t.migrated(ctx)
	if err != nil {
		return err
	}
	defer inst.Stop()
	defer func() { _ = db.Close() }()
	ddl, err := t.diff(ctx, inst)
	if err != nil {
		return err
	}
	if ddl != "" {
		return fmt.Errorf("migrations do not match db/schema; run make migration. Missing:\n%s", ddl)
	}
	_, err = fmt.Fprintln(t.out, "migrations OK")
	return err
}

// migrated starts a fresh cluster and applies all migrations with goose.
func (t tool) migrated(ctx context.Context) (*sql.DB, *pgtemp.Instance, error) {
	inst, err := pgtemp.Start(ctx)
	if err != nil {
		return nil, nil, err
	}
	db, err := sql.Open("pgx", inst.URL())
	if err != nil {
		inst.Stop()
		return nil, nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(t.dir))
	if err != nil && !errors.Is(err, goose.ErrNoMigrations) {
		_ = db.Close()
		inst.Stop()
		return nil, nil, fmt.Errorf("goose: %w", err)
	}
	if err == nil {
		if _, err := p.Up(ctx); err != nil {
			_ = db.Close()
			inst.Stop()
			return nil, nil, fmt.Errorf("apply migrations: %w", err)
		}
	}
	return db, inst, nil
}

// desired concatenates the schema files in name order.
func (t tool) desired() (string, error) {
	files, err := filepath.Glob(filepath.Join(t.schema, "*.sql"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no schema files in %s", t.schema)
	}
	var b strings.Builder
	for _, f := range files {
		c, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		b.Write(c)
		b.WriteString("\n")
	}
	return b.String(), nil
}

// diff returns the DDL psqldef would run to reach the desired schema, without
// comments, or "" when there is none.
func (t tool) diff(ctx context.Context, inst *pgtemp.Instance) (string, error) {
	want, err := t.desired()
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, t.psqldef, "-U", "birge", "-h", "127.0.0.1", "-p", strconv.Itoa(inst.Port),
		"--dry-run", "postgres")
	cmd.Stdin = strings.NewReader(want)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("psqldef: %w\n%s", err, stderr.String())
	}
	var keep []string
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		// goose wraps each migration in its own transaction.
		if trimmed == "" || strings.HasPrefix(trimmed, "--") || trimmed == "BEGIN;" || trimmed == "COMMIT;" {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n"), nil
}

var (
	approvedRe    = regexp.MustCompile(`(?m)^-- birge:destructive-approved ADR-\d{4}\b`)
	destructiveRe = regexp.MustCompile(`(?is)\b(drop\s+(table|column|schema|index|type|constraint|view|sequence|function|trigger)|truncate|alter\s+column\s+\S+\s+(set\s+data\s+)?type|set\s+not\s+null|delete\s+from\s+[^;]*?;)`)
	whereRe       = regexp.MustCompile(`(?is)\bwhere\b`)
	lockRe        = regexp.MustCompile(`(?im)^\s*set\s+lock_timeout\b`)
)

// gate returns a violation for a migration body, or "".
func gate(name, body string) string {
	code := stripComments(body)
	if strings.TrimSpace(code) != "" && !lockRe.MatchString(code) {
		return name + ": missing SET lock_timeout"
	}
	if approvedRe.MatchString(body) {
		return ""
	}
	for _, m := range destructiveRe.FindAllString(code, -1) {
		lower := strings.ToLower(m)
		if strings.HasPrefix(lower, "delete") && whereRe.MatchString(m) {
			continue
		}
		return fmt.Sprintf("%s: destructive statement %q needs '-- birge:destructive-approved ADR-NNNN'", name, strings.TrimSpace(m))
	}
	return ""
}

func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
