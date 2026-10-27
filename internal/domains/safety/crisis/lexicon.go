package crisis

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Languages the lexicon may contain (docs/requirements/mvp.md § 5: kk/ru/en).
const (
	LangKazakh  = "kk"
	LangRussian = "ru"
	LangEnglish = "en"
)

// FuzzyMinLen is the shortest normalised phrase word that also matches with
// one edit. Shorter words match exactly: one edit in a 4-letter word changes
// too much meaning.
const FuzzyMinLen = 5

// CompactMinLen is the shortest compact phrase (spaces removed) that is also
// searched as a substring of the compact message. Shorter phrases would match
// across unrelated word boundaries too often.
const CompactMinLen = 8

var (
	// Any language code ("kk", "ru", "en", "kk-Latn"): a new language is data, not code.
	fileNameRe = regexp.MustCompile(`^([a-z]{2,3}(?:-[A-Z][a-z]{3})?)\.([a-z][a-z_]{0,39})\.txt$`)
	// ErrEmptyLexicon is returned when the directory holds no entries at all:
	// a crisis detector that can never fire must stop the process, not run.
	ErrEmptyLexicon = errors.New("crisis: lexicon has no entries")
)

// Lexicon is a compiled, immutable crisis lexicon. The zero value and nil
// match nothing; production wiring must refuse to start without a loaded one.
type Lexicon struct {
	langs map[string][]*category // lang -> categories sorted by name
	size  int
}

type category struct {
	name    string
	phrases []phrase
	regexps []*regexp.Regexp
}

type phrase struct {
	words   []word
	compact string // "" when shorter than CompactMinLen
}

type word struct {
	runes  []rune
	prefix bool
}

// LoadDir loads a lexicon from the directory dir (configuration; see Load).
func LoadDir(dir string) (*Lexicon, error) {
	if dir == "" {
		return nil, errors.New("crisis: lexicon directory is not configured")
	}
	return Load(os.DirFS(dir))
}

// Load reads a lexicon from the root of fsys.
//
// Layout: one UTF-8 text file per language and category, named
// "<lang>.<category>.txt" where lang is a language code such as kk, ru, en or kk-Latn and category is
// [a-z][a-z_]*, for example "ru.suicide.txt". Other files (README, dot files,
// anything not ending in .txt) are ignored; a .txt file with any other name is
// an error so that a misnamed file is never silently skipped.
//
// Format, one entry per line:
//
//	# a comment (whole line only)
//	хочу умереть            a phrase; written naturally, normalised on load
//	покончит* с собой       '*' after a word: prefix match for that word
//	re: \sне хочу( \pL+)? жит\s
//	                        a regular expression (RE2) over the normalised
//	                        message, whose words are separated by single
//	                        spaces with one space before the first and after
//	                        the last word; mark word boundaries with \s
//	                        (\b is ASCII-only, and leading or trailing spaces
//	                        of the line are trimmed); must be written in the normalised
//	                        alphabet (no ё, й, ь, ъ, щ, э, Kazakh letters,
//	                        capitals) — the loader rejects those letters
//
// Blank lines are ignored. Any error names the file and line.
func Load(fsys fs.FS) (*Lexicon, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("crisis: read lexicon directory: %w", err)
	}
	lex := &Lexicon{langs: map[string][]*category{}}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || path.Ext(name) != ".txt" {
			continue
		}
		m := fileNameRe.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("crisis: %s: file name must be <lang>.<category>.txt", name)
		}
		cat, err := loadFile(fsys, name, m[2])
		if err != nil {
			return nil, err
		}
		lex.langs[m[1]] = append(lex.langs[m[1]], cat)
		lex.size += len(cat.phrases) + len(cat.regexps)
	}
	if lex.size == 0 {
		return nil, ErrEmptyLexicon
	}
	for _, cats := range lex.langs {
		sort.Slice(cats, func(i, j int) bool { return cats[i].name < cats[j].name })
	}
	return lex, nil
}

func loadFile(fsys fs.FS, name, catName string) (*category, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("crisis: open %s: %w", name, err)
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	cat := &category{name: catName}
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if !utf8.ValidString(raw) {
			return nil, fmt.Errorf("crisis: %s:%d: not valid UTF-8", name, line)
		}
		if expr, ok := strings.CutPrefix(raw, "re:"); ok {
			re, err := compileRegexp(strings.TrimSpace(expr))
			if err != nil {
				return nil, fmt.Errorf("crisis: %s:%d: %w", name, line, err)
			}
			cat.regexps = append(cat.regexps, re)
			continue
		}
		p, err := compilePhrase(raw)
		if err != nil {
			return nil, fmt.Errorf("crisis: %s:%d: %w", name, line, err)
		}
		cat.phrases = append(cat.phrases, p)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("crisis: read %s: %w", name, err)
	}
	return cat, nil
}

func compilePhrase(raw string) (phrase, error) {
	var p phrase
	for _, tok := range strings.Fields(raw) {
		prefix := strings.HasSuffix(tok, "*")
		ws := Normalize(strings.TrimRight(tok, "*")).Words
		for i, w := range ws {
			p.words = append(p.words, word{runes: []rune(w), prefix: prefix && i == len(ws)-1})
		}
	}
	if len(p.words) == 0 {
		return phrase{}, errors.New("phrase has no letters after normalisation")
	}
	var b strings.Builder
	for _, w := range p.words {
		b.WriteString(string(w.runes))
	}
	if c := collapse(b.String()); utf8.RuneCountInString(c) >= CompactMinLen {
		p.compact = c
	}
	return p, nil
}

func compileRegexp(expr string) (*regexp.Regexp, error) {
	if expr == "" {
		return nil, errors.New("empty regular expression")
	}
	for _, r := range expr {
		if r < utf8.RuneSelf || !unicode.IsLetter(r) {
			continue // ASCII is regex syntax (\s, \pL, ...) or Latin text
		}
		if unicode.IsUpper(r) {
			return nil, fmt.Errorf("regular expression contains %q: write it in lower case", r)
		}
		if v, ok := variants[r]; ok && v != r {
			return nil, fmt.Errorf("regular expression contains %q, which normalisation removes; write the normalised form", r)
		}
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("regular expression: %w", err)
	}
	return re, nil
}

// Size returns the number of phrases and regular expressions loaded.
func (l *Lexicon) Size() int {
	if l == nil {
		return 0
	}
	return l.size
}

// Languages returns the languages that have at least one category, sorted.
func (l *Lexicon) Languages() []string {
	if l == nil {
		return nil
	}
	out := make([]string, 0, len(l.langs))
	for lang := range l.langs {
		out = append(out, lang)
	}
	sort.Strings(out)
	return out
}
