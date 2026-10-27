package crisis

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func demoLexicon(t testing.TB) *Lexicon {
	t.Helper()
	lex, err := LoadDir(filepath.Join("testdata", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	return lex
}

type evalCase struct {
	file     string
	line     int
	expected string
	lang     string
	text     string
}

func loadEvalSet(t *testing.T) []evalCase {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "..", "evals", "datasets", "crisis")
	files, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no evaluation files in %s", dir)
	}
	var cases []evalCase
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		n := 0
		for sc.Scan() {
			n++
			line := sc.Text()
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) != 3 || (parts[0] != "crisis" && parts[0] != "none") || (parts[1] != LangKazakh && parts[1] != LangRussian && parts[1] != LangEnglish) {
				t.Fatalf("%s:%d: want expected<TAB>lang<TAB>text", filepath.Base(path), n)
			}
			cases = append(cases, evalCase{file: filepath.Base(path), line: n, expected: parts[0], lang: parts[1], text: parts[2]})
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	return cases
}

// TestDemoLexiconEvalSet is the deterministic L1 gate of ADR-0017 run against
// the DEMO lexicon: 100% recall on crisis cases, false-positive rate logged.
func TestDemoLexiconEvalSet(t *testing.T) {
	lex := demoLexicon(t)
	cases := loadEvalSet(t)
	var crisisTotal, crisisHit, noneTotal, noneHit int
	for _, c := range cases {
		got := lex.Detect(c.text, c.lang)
		switch c.expected {
		case "crisis":
			crisisTotal++
			if got.Crisis {
				crisisHit++
			} else {
				// The case text is synthetic; naming the line is enough.
				t.Errorf("%s:%d: crisis case not detected", c.file, c.line)
			}
		case "none":
			noneTotal++
			if got.Crisis {
				noneHit++
				t.Logf("%s:%d: false positive (category %s/%s)", c.file, c.line, got.Lang, got.Category)
			}
		}
	}
	if crisisTotal == 0 || noneTotal == 0 {
		t.Fatalf("evaluation set needs both crisis and none cases: %d/%d", crisisTotal, noneTotal)
	}
	t.Logf("crisis recall: %d/%d (%.1f%%)", crisisHit, crisisTotal, 100*float64(crisisHit)/float64(crisisTotal))
	t.Logf("false-positive rate: %d/%d (%.1f%%)", noneHit, noneTotal, 100*float64(noneHit)/float64(noneTotal))
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"ХОЧУ УМЕРЕТЬ!!!", " хочу умерет "},
		{"хочу умерееееть", " хочу умерет "},
		{"хочу умeреть", " хочу умерет "},          // Latin e
		{"ЁЛКА Йод Эх щи", " елка иод ех ши "},     // spelling variants
		{"өлгiм келедi", " олгим келеди "},         // Kazakh + Latin i
		{"Өмірдің мәні жоқ", " омирдин мани жок "}, // Kazakh letters
		{"4то-то", " что то "},                     // digit look-alike, hyphen splits
		{"OK, cop мир", " ок сор мир "},            // all-look-alike words in a Cyrillic message
		{"hello мир", " helo мир "},                // Latin word stays Latin (repeats still collapse)
		{"hello world", " helo world "},            // no Cyrillic: nothing mapped
		{"у\u0301мер", " умер "},                   // combining accent removed
		{"у\u200bмер\u00adеть", " умерет "},        // zero-width space and soft hyphen
		{"   ", "  "},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in).Spaced; got != tt.want {
			t.Errorf("Normalize(%q).Spaced = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := Normalize("х о ч у  у м е р е т ь").Compact; got != "хочумерет" {
		t.Errorf("compact = %q", got)
	}
}

func TestDetectCategoriesAndLanguages(t *testing.T) {
	lex := demoLexicon(t)
	tests := []struct {
		text, lang       string
		crisis           bool
		category, gotLng string
	}{
		{"я хочу умереть", "ru", true, "suicide", "ru"},
		{"я хочу умереть", "kk", true, "suicide", "ru"}, // other language still checked
		{"өлгім келеді", "ru", true, "suicide", "kk"},
		{"муж избивает меня", "ru", true, "violence", "ru"},
		{"порезала себе запястья", "ru", true, "self_harm", "ru"},
		{"всё хорошо, спасибо", "ru", false, "", ""},
		{"", "ru", false, "", ""},
		{"!!! ...", "ru", false, "", ""},
		{"хочу умереть", "en", true, "suicide", "ru"}, // unknown language: all checked
	}
	for _, tt := range tests {
		got := lex.Detect(tt.text, tt.lang)
		if got.Crisis != tt.crisis || got.Category != tt.category || got.Lang != tt.gotLng {
			t.Errorf("Detect(case %q/%s) = %+v", tt.category, tt.lang, got)
		}
	}
}

func TestNilAndEmptyLexicon(t *testing.T) {
	var lex *Lexicon
	if lex.Detect("хочу умереть", "ru").Crisis {
		t.Fatal("nil lexicon matched")
	}
	if lex.Size() != 0 || lex.Languages() != nil {
		t.Fatal("nil lexicon has entries")
	}
	_, err := Load(fstest.MapFS{"ru.suicide.txt": {Data: []byte("# only a comment\n\n")}})
	if !errors.Is(err, ErrEmptyLexicon) {
		t.Fatalf("empty lexicon: err = %v", err)
	}
	if _, err := LoadDir(""); err == nil {
		t.Fatal("unconfigured directory accepted")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"bad file name":      {"suicide.txt": {Data: []byte("хочу умереть\n")}},
		"malformed language": {"RU.suicide.txt": {Data: []byte("хочу умереть\n")}},
		"long language":      {"russian.suicide.txt": {Data: []byte("хочу умереть\n")}},
		"bad regex":          {"ru.suicide.txt": {Data: []byte("re: (\n")}},
		"empty regex":        {"ru.suicide.txt": {Data: []byte("re:\n")}},
		"regex with ё":       {"ru.suicide.txt": {Data: []byte("re: ёлка\n")}},
		"regex with ь":       {"ru.suicide.txt": {Data: []byte("re: умереть\n")}},
		"regex upper case":   {"ru.suicide.txt": {Data: []byte("re: Умер\n")}},
		"regex Kazakh":       {"kk.suicide.txt": {Data: []byte("re: өлгім\n")}},
		"phrase only noise":  {"ru.suicide.txt": {Data: []byte("!!!\n")}},
		"invalid utf8":       {"ru.suicide.txt": {Data: []byte("\xff\xfe\n")}},
	}
	for name, fsys := range tests {
		if _, err := Load(fsys); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	lex, err := Load(fstest.MapFS{
		"ru.suicide.txt": {Data: []byte("хочу умереть\n")},
		"README.md":      {Data: []byte("ignored")},
		".hidden.txt":    {Data: []byte("ignored")},
		"ru.x.txt/inner": {Data: []byte("directory, ignored")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if lex.Size() != 1 || strings.Join(lex.Languages(), ",") != "ru" {
		t.Fatalf("size=%d langs=%v", lex.Size(), lex.Languages())
	}
}

func TestWordMatching(t *testing.T) {
	lex, err := Load(fstest.MapFS{
		"ru.test.txt": {Data: []byte("покончит* с собой\nхочу умереть\nre: \\sне хочу( \\pL+)? жит\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		text string
		want bool
	}{
		{"покончила с собой", true},   // prefix + one substitution
		{"покончить с собой", true},   // exact prefix
		{"пакончить с собой", true},   // typo in prefix word
		{"хочу умреть", true},         // deletion
		{"хочу умерть", true},         // deletion
		{"хочу умеретььь", true},      // soft signs dropped
		{"хочу уемреть", true},        // transposition
		{"хочу ушереть", true},        // substitution
		{"хочу уехать", false},        // two or more edits
		{"хачу умереть", false},       // short words are exact
		{"не хочу больше жить", true}, // regex with an optional word
		{"мне хочу жить", false},      // regex boundary
		{"не хочу совсем никак жить", false},
	}
	for _, tt := range tests {
		if got := lex.Detect(tt.text, "ru").Crisis; got != tt.want {
			t.Errorf("Detect(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

func TestWithinOne(t *testing.T) {
	tests := []struct {
		pat, in string
		prefix  bool
		want    bool
	}{
		{"умерет", "умерет", false, true},
		{"умерет", "умрет", false, true},   // deletion
		{"умерет", "умеррет", false, true}, // insertion (before collapse)
		{"умерет", "уемрет", false, true},  // transposition
		{"умерет", "умерот", false, true},  // substitution
		{"умерет", "умере", false, true},   // last letter missing
		{"умерет", "умеретх", false, true}, // one extra letter at the end
		{"умерет", "умеретхх", false, false},
		{"умерет", "умр", false, false},
		{"умерет", "", false, false},
		{"умерет", "уехат", false, false},
		{"покончит", "покончила", true, true},
		{"покончит", "покончитьбы", true, true},
		{"покончит", "пакончил", true, false}, // two edits
		{"покончит", "покончи", true, true},
		{"абвгд", "где", true, false},
	}
	for _, tt := range tests {
		if got := withinOne([]rune(tt.pat), []rune(tt.in), tt.prefix); got != tt.want {
			t.Errorf("withinOne(%q, %q, %v) = %v, want %v", tt.pat, tt.in, tt.prefix, got, tt.want)
		}
	}
}

func BenchmarkDetect(b *testing.B) {
	lex := demoLexicon(b)
	text := strings.Repeat("Сегодня был тяжёлый день, ребёнок плохо спал, я очень устала. ", 8)
	b.ReportAllocs()
	for b.Loop() {
		_ = lex.Detect(text, "ru")
	}
}
