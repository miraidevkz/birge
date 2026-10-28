// Command birge-coverage is the coverage ratchet: it reads one Go coverage
// profile (go test -coverprofile over the whole module), sums it per bounded
// context (internal/domains/<context>) and for internal/platform, and compares
// every sum with the floor committed in coverage-floor.txt.
//
//	birge-coverage -profile bin/coverage.out             check against the floors
//	birge-coverage -profile bin/coverage.out -update     raise the floors to the current numbers
//
// A context fails when it drops more than -tolerance points below its floor
// (0.5 by default: concurrent tests take slightly different paths from run to
// run). A context without a floor and a floor without a context also fail, so
// a new context cannot slip in unmeasured and a removed one does not leave a
// stale line. -update never lowers a floor; lowering one is a reviewed edit of
// the file.
//
// Numbers are statement-weighted over all packages of the context, packages
// without tests counting as 0%. Generated files (the standard "Code generated
// ... DO NOT EDIT." header: oapi-codegen, sqlc) are left out: nobody writes
// tests for them line by line, and their size would hide the hand-written code.
// So are test-support packages (a directory named <something>test, such as
// chattest): fakes for other packages' tests, not production code.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// exitUsage is the status for bad flags or unreadable input; 1 means a floor
// was missed.
const exitUsage = 2

func main() {
	profile := flag.String("profile", "bin/coverage.out", "coverage profile written by go test -coverprofile")
	floorFile := flag.String("floor", "coverage-floor.txt", "committed per-context minimums")
	tolerance := flag.Float64("tolerance", 0.5, "points a context may fall below its floor before it fails")
	update := flag.Bool("update", false, "raise the floors to the current numbers (never lowers one)")
	root := flag.String("root", ".", "module root (holds go.mod and the sources)")
	flag.Parse()

	code, err := run(*root, *profile, *floorFile, *tolerance, *update, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "birge-coverage:", err)
	}
	os.Exit(code)
}

func run(root, profile, floorFile string, tolerance float64, update bool, out io.Writer) (int, error) {
	module, err := modulePath(filepath.Join(root, "go.mod"))
	if err != nil {
		return exitUsage, err
	}
	pf, err := os.Open(profile)
	if err != nil {
		return exitUsage, fmt.Errorf("open profile (run make go-test first): %w", err)
	}
	defer func() { _ = pf.Close() }() // read-only: nothing to flush
	blocks, err := parseProfile(pf)
	if err != nil {
		return exitUsage, err
	}
	gen := generatedChecker(root, module)
	current, err := perContext(blocks, module, gen)
	if err != nil {
		return exitUsage, err
	}

	floors := map[string]float64{}
	if data, err := os.ReadFile(floorFile); err == nil {
		if floors, err = parseFloors(string(data)); err != nil {
			return exitUsage, fmt.Errorf("%s: %w", floorFile, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) || !update {
		return exitUsage, fmt.Errorf("read floors: %w", err)
	}

	if update {
		raised := raise(floors, current)
		if err := os.WriteFile(floorFile, []byte(formatFloors(raised)), 0o644); err != nil {
			return exitUsage, fmt.Errorf("write floors: %w", err)
		}
		floors = raised
	}

	rows, failed := compare(current, floors, tolerance)
	if _, err := io.WriteString(out, formatTable(rows)); err != nil {
		return exitUsage, fmt.Errorf("write report: %w", err)
	}
	if failed {
		return 1, errors.New("coverage below the floor (see FAIL rows); add tests, or lower the floor in a reviewed change")
	}
	return 0, nil
}

// counts is the statement total and the covered part for one context.
type counts struct {
	stmts, covered int
}

func (c counts) percent() float64 {
	if c.stmts == 0 {
		return 0
	}
	return 100 * float64(c.covered) / float64(c.stmts)
}

// block is one line of a coverage profile.
type block struct {
	file  string // import path of the package plus the file name
	pos   string // "line.col,line.col"
	stmts int
	count int
}

// parseProfile reads a coverage profile. One block can appear several times
// (once per test binary that instrumented the package); it is merged, covered
// when any line says so.
func parseProfile(r io.Reader) ([]block, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	index := map[string]int{}
	var blocks []block
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		b, err := parseBlock(line)
		if err != nil {
			return nil, fmt.Errorf("profile line %d: %w", lineNo, err)
		}
		key := b.file + ":" + b.pos
		if i, ok := index[key]; ok {
			blocks[i].count = max(blocks[i].count, b.count)
			continue
		}
		index[key] = len(blocks)
		blocks = append(blocks, b)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read profile: %w", err)
	}
	return blocks, nil
}

func parseBlock(line string) (block, error) {
	// file:start.col,end.col numStmts count; the file part may contain ':' only
	// on Windows, so split at the last one.
	colon := strings.LastIndex(line, ":")
	if colon < 0 {
		return block{}, errors.New("no ':'")
	}
	fields := strings.Fields(line[colon+1:])
	if len(fields) != 3 {
		return block{}, errors.New("want position, statements and count")
	}
	stmts, err := strconv.Atoi(fields[1])
	if err != nil {
		return block{}, fmt.Errorf("statements: %w", err)
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil {
		return block{}, fmt.Errorf("count: %w", err)
	}
	return block{file: line[:colon], pos: fields[0], stmts: stmts, count: count}, nil
}

// contextOf names the ratchet bucket of a source file relative to the module
// root, or "" for code outside the measured areas (cmd, wiring, archtest).
func contextOf(rel string) string {
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) >= 4 && parts[0] == "internal" && parts[1] == "domains":
		return parts[2]
	case len(parts) >= 3 && parts[0] == "internal" && parts[1] == "platform":
		return "platform"
	}
	return ""
}

// isTestSupport reports files of a test-support package (the fakes in
// <context>test, for example chattest): they run only under tests, so counting
// them would lower a context for code that is not production code.
func isTestSupport(rel string) bool {
	return strings.HasSuffix(path.Base(path.Dir(rel)), "test")
}

func perContext(blocks []block, module string, generated func(rel string) (bool, error)) (map[string]counts, error) {
	out := map[string]counts{}
	for _, b := range blocks {
		rel, ok := strings.CutPrefix(b.file, module+"/")
		if !ok {
			continue
		}
		ctx := contextOf(rel)
		if ctx == "" || isTestSupport(rel) {
			continue
		}
		gen, err := generated(rel)
		if err != nil {
			return nil, err
		}
		if gen {
			continue
		}
		c := out[ctx]
		c.stmts += b.stmts
		if b.count > 0 {
			c.covered += b.stmts
		}
		out[ctx] = c
	}
	return out, nil
}

// generatedRE is the convention of https://go.dev/s/generatedcode.
var generatedRE = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// generatedChecker reports whether a module file carries the generated-code
// header before its package clause, reading each file once.
func generatedChecker(root, module string) func(rel string) (bool, error) {
	cache := map[string]bool{}
	return func(rel string) (bool, error) {
		if v, ok := cache[rel]; ok {
			return v, nil
		}
		f, err := os.Open(filepath.Join(root, filepath.FromSlash(path.Clean(rel))))
		if err != nil {
			return false, fmt.Errorf("source of %s/%s: %w", module, rel, err)
		}
		defer func() { _ = f.Close() }() // read-only: nothing to flush
		gen := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "package ") {
				break
			}
			if generatedRE.MatchString(line) {
				gen = true
				break
			}
		}
		if err := sc.Err(); err != nil {
			return false, fmt.Errorf("read %s: %w", rel, err)
		}
		cache[rel] = gen
		return gen, nil
	}
}

func modulePath(goMod string) (string, error) {
	data, err := os.ReadFile(goMod)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for line := range strings.Lines(string(data)) {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`), nil
		}
	}
	return "", errors.New("go.mod has no module line")
}

// parseFloors reads "<context> <percent>" lines; '#' starts a comment.
func parseFloors(s string) (map[string]float64, error) {
	floors := map[string]float64{}
	n := 0
	for line := range strings.Lines(s) {
		n++
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d: want '<context> <percent>'", n)
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || v < 0 || v > 100 {
			return nil, fmt.Errorf("line %d: percent must be a number from 0 to 100", n)
		}
		if _, dup := floors[fields[0]]; dup {
			return nil, fmt.Errorf("line %d: %s listed twice", n, fields[0])
		}
		floors[fields[0]] = v
	}
	return floors, nil
}

const floorHeader = `# Coverage ratchet: minimum statement coverage per bounded context
# (internal/domains/<context>) and for internal/platform, in percent, generated
# files excluded. make coverage-check fails when a context falls more than 0.5
# points below its line. Raise with make coverage-floor; lower only in a
# reviewed change that says why. See docs/development.md, "Coverage ratchet".
`

func formatFloors(floors map[string]float64) string {
	var b strings.Builder
	b.WriteString(floorHeader)
	for _, name := range sortedKeys(floors) {
		fmt.Fprintf(&b, "%-12s %.1f\n", name, floors[name])
	}
	return b.String()
}

// raise returns the floors moved up to the current numbers, rounded down to a
// tenth so a later identical run never fails on rounding. Contexts that no
// longer exist are dropped; new ones get their current number.
func raise(floors map[string]float64, current map[string]counts) map[string]float64 {
	out := map[string]float64{}
	for name, c := range current {
		now := math.Floor(c.percent()*10) / 10
		out[name] = max(now, floors[name])
	}
	return out
}

// row is one line of the report table.
type row struct {
	name     string
	stmts    int
	percent  float64
	floor    float64
	hasFloor bool
	present  bool
	status   string
}

func compare(current map[string]counts, floors map[string]float64, tolerance float64) ([]row, bool) {
	names := map[string]bool{}
	for n := range current {
		names[n] = true
	}
	for n := range floors {
		names[n] = true
	}
	failed := false
	var rows []row
	for _, name := range sortedKeys(names) {
		c, present := current[name]
		f, hasFloor := floors[name]
		r := row{name: name, stmts: c.stmts, percent: c.percent(), floor: f, hasFloor: hasFloor, present: present}
		switch {
		case !present:
			r.status = "FAIL no code measured; remove its floor"
		case !hasFloor:
			r.status = "FAIL no floor; run make coverage-floor"
		case r.percent < f-tolerance:
			r.status = fmt.Sprintf("FAIL %.1f below the floor", f-r.percent)
		default:
			r.status = "ok"
		}
		if r.status != "ok" {
			failed = true
		}
		rows = append(rows, r)
	}
	return rows, failed
}

func formatTable(rows []row) string {
	var w strings.Builder
	fmt.Fprintf(&w, "%-12s %8s %8s %8s  %s\n", "context", "stmts", "cover%", "floor%", "status")
	for _, r := range rows {
		floor := "-"
		if r.hasFloor {
			floor = fmt.Sprintf("%.1f", r.floor)
		}
		cover := "-"
		if r.present {
			cover = fmt.Sprintf("%.1f", r.percent)
		}
		fmt.Fprintf(&w, "%-12s %8d %8s %8s  %s\n", r.name, r.stmts, cover, floor, r.status)
	}
	return w.String()
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
