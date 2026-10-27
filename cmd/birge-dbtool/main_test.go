package main

import (
	"strings"
	"testing"
)

func TestGate(t *testing.T) {
	ok := "-- +goose Up\n" + header
	tests := []struct {
		name, body, want string
	}{
		{"additive", ok + "CREATE TABLE x (id int);\nALTER TABLE x ADD COLUMN y text;", ""},
		{"no lock timeout", "-- +goose Up\nCREATE TABLE x (id int);", "lock_timeout"},
		{"drop column", ok + "ALTER TABLE x DROP COLUMN y;", "destructive"},
		{"drop table", ok + "DROP TABLE x;", "destructive"},
		{"truncate", ok + "TRUNCATE x;", "destructive"},
		{"type change", ok + "ALTER TABLE x ALTER COLUMN y TYPE int;", "destructive"},
		{"not null", ok + "ALTER TABLE x ALTER COLUMN y SET NOT NULL;", "destructive"},
		{"delete without where", ok + "DELETE FROM x;", "destructive"},
		{"delete with where", ok + "DELETE FROM x WHERE id = 1;", ""},
		{"approved", "-- birge:destructive-approved ADR-0042\n" + ok + "DROP TABLE x;", ""},
		{"commented drop", ok + "-- DROP TABLE x;\nCREATE TABLE y (id int);", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gate("m.sql", tt.body)
			if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("gate() = %q, want containing %q", got, tt.want)
			}
		})
	}
}
