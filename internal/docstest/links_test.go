// Package docstest checks the repository's Markdown documentation on every
// test run, so a moved file or a renamed heading cannot leave a reader at a
// dead link. It reads only files in the repository: no network, no tool.
package docstest

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// skipDirs are never documentation of this project: dependencies, build
// output and vendored skills, whose links point into their upstream repos.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "bin": true, "dist": true,
	".expo": true, "skills": true,
}

var (
	fence   = regexp.MustCompile("(?s)```.*?```")
	comment = regexp.MustCompile(`(?s)<!--.*?-->`)
	link    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	heading = regexp.MustCompile(`(?m)^#{1,6}\s+(.+?)\s*$`)
	scheme  = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:`)
)

// TestRelativeLinks fails on a relative link to a file that does not exist
// or to a heading that the target Markdown file does not have. External
// links (http:, mailto:, …) are not checked: that would need the network.
func TestRelativeLinks(t *testing.T) {
	root := filepath.Join("..", "..")
	anchors := map[string]map[string]bool{}
	var broken []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := comment.ReplaceAllString(fence.ReplaceAllString(string(raw), ""), "")
		rel, _ := filepath.Rel(root, path)
		for _, m := range link.FindAllStringSubmatch(text, -1) {
			target := m[1]
			if scheme.MatchString(target) {
				continue
			}
			file, anchor, _ := strings.Cut(target, "#")
			dest := path
			if file != "" {
				dest = filepath.Join(filepath.Dir(path), filepath.FromSlash(file))
				if _, err := os.Stat(dest); err != nil {
					broken = append(broken, rel+": "+target)
					continue
				}
			}
			if anchor == "" || !strings.HasSuffix(dest, ".md") {
				continue
			}
			set, ok := anchors[dest]
			if !ok {
				set = headingAnchors(t, dest)
				anchors[dest] = set
			}
			if !set[strings.ToLower(anchor)] {
				broken = append(broken, rel+": "+target+" (no such heading)")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range broken {
		t.Error("broken link in " + b)
	}
}

// headingAnchors returns the anchors GitHub generates for the headings of
// a Markdown file: lower case, punctuation dropped, spaces as hyphens.
func headingAnchors(t *testing.T, path string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range heading.FindAllStringSubmatch(fence.ReplaceAllString(string(raw), ""), -1) {
		var b strings.Builder
		for _, r := range strings.ToLower(m[1]) {
			switch {
			case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_':
				b.WriteRune(r)
			case r == ' ':
				b.WriteRune('-')
			}
		}
		out[b.String()] = true
	}
	return out
}
