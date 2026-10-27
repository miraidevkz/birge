package db

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestRolesCoverEverySchema fails when a schema in db/schema has no grant in
// roles.sql: the application role would get "permission denied" in
// production while tests that connect as a superuser stay green.
func TestRolesCoverEverySchema(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("schema", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("schema files: %v (%d)", err, len(files))
	}
	create := regexp.MustCompile(`(?im)^\s*CREATE SCHEMA(?: IF NOT EXISTS)?\s+([a-z_]+)`)
	seen := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range create.FindAllStringSubmatch(string(b), -1) {
			seen++
			if !strings.Contains(Roles, "GRANT USAGE ON SCHEMA "+m[1]+" TO birge_app;") {
				t.Errorf("%s: schema %s has no GRANT USAGE in roles.sql", filepath.Base(f), m[1])
			}
		}
	}
	if seen == 0 {
		t.Fatal("no CREATE SCHEMA found in db/schema")
	}
}
