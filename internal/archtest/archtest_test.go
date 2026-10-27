// Package archtest enforces the module boundaries of ADR-0002 on every test run.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "birge/"

// Rules:
//   - a bounded context imports another context only through its exported
//     service package: birge/internal/domains/<other>/service;
//   - internal/platform never imports a domain or cmd;
//   - nothing imports cmd.
func TestModuleBoundaries(t *testing.T) {
	root := filepath.Join("..", "..")
	var violations []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			if !strings.HasPrefix(p, module) {
				continue
			}
			if v := check(rel, strings.TrimPrefix(p, module)); v != "" {
				violations = append(violations, rel+": "+v)
			}
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}

// check returns a violation for file (repo-relative) importing pkg (module-relative), or "".
func check(file, pkg string) string {
	if strings.HasPrefix(pkg, "cmd/") {
		return "imports " + pkg + ": nothing may import cmd"
	}
	fromCtx := contextOf(file)
	toCtx := contextOf(pkg + "/")
	switch {
	case strings.HasPrefix(file, "internal/platform/") && toCtx != "":
		return "platform imports domain " + pkg
	case fromCtx != "" && toCtx != "" && fromCtx != toCtx && pkg != "internal/domains/"+toCtx+"/service":
		return "imports " + pkg + ": other contexts are reachable only through internal/domains/" + toCtx + "/service"
	}
	return ""
}

// contextOf returns the bounded context of a repo-relative path, or "".
func contextOf(p string) string {
	rest, ok := strings.CutPrefix(p, "internal/domains/")
	if !ok {
		return ""
	}
	ctx, _, _ := strings.Cut(rest, "/")
	return ctx
}

func TestCheck(t *testing.T) {
	tests := []struct{ file, pkg, wantSub string }{
		{"internal/domains/safety/service/s.go", "internal/domains/safety/model", ""},
		{"internal/domains/safety/service/s.go", "internal/domains/notify/service", ""},
		{"internal/domains/safety/service/s.go", "internal/domains/notify/adapter", "only through"},
		{"internal/domains/safety/service/s.go", "internal/platform/outbox", ""},
		{"internal/platform/outbox/o.go", "internal/domains/safety/model", "platform imports domain"},
		{"internal/domains/safety/api/h.go", "cmd/birge", "nothing may import cmd"},
	}
	for _, tt := range tests {
		got := check(tt.file, tt.pkg)
		if (tt.wantSub == "") != (got == "") || !strings.Contains(got, tt.wantSub) {
			t.Errorf("check(%q, %q) = %q, want containing %q", tt.file, tt.pkg, got, tt.wantSub)
		}
	}
}
