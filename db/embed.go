// Package db embeds the versioned migrations and the privileges script into
// the binary, so `birge migrate` applies exactly the files it was built with
// (ADR-0006) and needs no checkout of the repository at run time.
//
// Migrations are generated from db/schema and never edited after merge
// (db/migrations/README.md); this package only exposes them.
package db

import (
	"embed"
	"io/fs"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Roles is db/roles.sql: grants for the application role, applied after the
// migrations only when the operator asks for it (BIRGE_APPLY_ROLES=1).
//
//go:embed roles.sql
var Roles string

// Migrations returns the goose migrations with the files at the root.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		// fs.Sub fails only for an invalid literal path; this is a build defect.
		panic(err)
	}
	return sub
}
