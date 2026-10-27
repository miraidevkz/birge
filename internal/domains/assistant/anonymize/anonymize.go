package anonymize

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Labels that replace personal data. The system prompt explains them to the model.
const (
	LabelName    = "[NAME]"
	LabelPhone   = "[PHONE]"
	LabelIIN     = "[IIN]"
	LabelEmail   = "[EMAIL]"
	LabelAddress = "[ADDRESS]"
	LabelDoc     = "[DOC]"
)

var (
	emailRe = regexp.MustCompile(`[\p{L}\p{N}._%+\-]+[ \t]?@[ \t]?[\p{L}\p{N}\-]+(?:\.[\p{L}\p{N}\-]+)*\.\p{L}{2,}`)

	// Street keyword before the street name: abbreviations need a dot or a
	// space after them so that ordinary words (улыбка) never match.
	addrPrefix = `(?i:(?:ул|пр|пр-т|просп|пер|мкр|мкрн|б-р|наб|пл|бул|st|ave|rd|blvd|көш|кош|даңғ|данғ|данг)\.[ \t]*` +
		`|(?:ул|мкр|мкрн|пр-т|б-р)[ \t]+` +
		`|(?:улиц\p{L}*|проспект\p{L}*|переул\p{L}*|микрорайон\p{L}*|бульвар\p{L}*|шоссе|набережн\p{L}*|площад\p{L}*|street|avenue|road|lane|boulevard)[ \t]+)`
	addrHouse = `(?:(?i:д\.|дом|house|no\.?|№|үй|уй)[ \t]*)?\d+\p{L}?` +
		`(?:[ \t]*[,/]?[ \t]*(?i:корп\.?|к\.|стр\.)[ \t]*\d+)?` +
		`(?:[ \t]*,?[ \t]*(?i:үй|уй))?` +
		`(?:[ \t]*,?[ \t]*(?i:кв\.?|квартира|apt\.?|flat|пәтер|патер)[ \t]*\d+)?`
	addrStreetRe = regexp.MustCompile(addrPrefix +
		`(?:[\p{L}\p{N}][\p{L}\p{N}\-.]*(?:[ \t]+[\p{L}][\p{L}\-.]*){0,2}?[ \t,]+)?` + addrHouse)
	addrKazakhRe = regexp.MustCompile(`[\p{L}][\p{L}\-]*[ \t]+` +
		`(?i:к[өо]шес[іи]\p{L}*|к-с[іи]|да[ңн][ғг][ыи]лы\p{L}*|ша[ғг]ын[ \t]+аудан\p{L}*|ықшамаудан\p{L}*|ыкшамаудан\p{L}*|м[өо]лтек[ \t]*аудан\p{L}*)` +
		`[ \t,]*` + addrHouse)
	addrEnglishRe = regexp.MustCompile(`\d+\p{L}?[ \t]+(?:\p{Lu}[\p{L}\-]*[ \t]+){1,3}` +
		`(?i:street|st\.?|avenue|ave\.?|road|rd\.?|lane|ln\.?|boulevard|blvd\.?|drive|dr\.?)`)
	addrEnglishPostRe = regexp.MustCompile(`\p{Lu}[\p{L}\-]*[ \t]+` +
		`(?i:street|st\.|avenue|ave\.|road|rd\.|lane|boulevard|blvd\.)[ \t,]*` + addrHouse)
	addrFlatRe = regexp.MustCompile(`(?i:д\.|дом|үй|уй)[ \t]*\d+\p{L}?[ \t]*,?[ \t]*(?i:кв\.?|квартира|пәтер|патер)[ \t]*\d+`)

	docPrefixedRe = regexp.MustCompile(`[A-ZА-ЯЁ]{1,2}[ \t]?\d{6,9}`)

	digitRunRe   = regexp.MustCompile(`\+?[\d(][\d \t\x{00A0}().\-]*\d`)
	dateRe       = regexp.MustCompile(`^\d{1,2}[./\-]\d{1,2}[./\-](?:\d{2}|\d{4})$`)
	yearRangeRe  = regexp.MustCompile(`^\d{4}[ \t]?-[ \t]?\d{4}$`)
	thousandsRe  = regexp.MustCompile(`^\d{1,3}(?:[ \x{00A0}]\d{3})+$`)
	localPhoneRe = regexp.MustCompile(`^\d{2,3}-\d{2}-\d{2}$`)
)

// Anonymizer replaces personal data with labels. Build it once with New.
type Anonymizer struct {
	dict nameIndex
}

// New returns an Anonymizer for a dictionary of common names (any case, any
// of kk/ru/en). Names shorter than three letters are ignored in the
// dictionary because they collide with ordinary words.
func New(names []string) *Anonymizer {
	idx := newNameIndex()
	for _, n := range names {
		idx.add(n, 3)
	}
	return &Anonymizer{dict: idx}
}

// ParseNameList reads one name per line; blank lines and lines starting with
// '#' are skipped.
func ParseNameList(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("anonymize: read name list: %w", err)
	}
	return out, nil
}

// Anonymize returns text with personal data replaced by labels. ownNames are
// the member's own names (display name); each word of each is matched in
// its case forms, including two-letter names. A nil Anonymizer still
// replaces everything except dictionary names.
func (a *Anonymizer) Anonymize(text string, ownNames ...string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	s := replaceBounded(emailRe, text, func(string) (string, bool) { return LabelEmail, true })
	for _, re := range []*regexp.Regexp{addrKazakhRe, addrEnglishPostRe, addrStreetRe, addrEnglishRe, addrFlatRe} {
		s = replaceBounded(re, s, func(string) (string, bool) { return LabelAddress, true })
	}
	s = replaceBounded(docPrefixedRe, s, func(string) (string, bool) { return LabelDoc, true })
	s = replaceBounded(digitRunRe, s, classifyDigits)

	own := newNameIndex()
	for _, n := range ownNames {
		for _, part := range strings.FieldsFunc(n, func(r rune) bool { return !isWordRune(r) }) {
			own.add(part, 2)
		}
	}
	var dict *nameIndex
	if a != nil {
		dict = &a.dict
	}
	return replaceNames(s, &own, dict)
}

// classifyDigits labels a run of digits by its digit count (see package doc).
func classifyDigits(m string) (string, bool) {
	var digits []byte
	for i := 0; i < len(m); i++ {
		if m[i] >= '0' && m[i] <= '9' {
			digits = append(digits, m[i])
		}
	}
	d := len(digits)
	trimmed := strings.TrimSpace(m)
	switch {
	case d == 12:
		return LabelIIN, true
	case strings.HasPrefix(trimmed, "+") && d >= 7:
		return LabelPhone, true
	case d == 11 && (digits[0] == '7' || digits[0] == '8'):
		return LabelPhone, true
	case d == 10 && digits[0] == '7':
		return LabelPhone, true
	case localPhoneRe.MatchString(trimmed):
		return LabelPhone, true
	case dateRe.MatchString(trimmed), yearRangeRe.MatchString(trimmed), thousandsRe.MatchString(trimmed):
		return "", false
	case d >= 7:
		return LabelDoc, true
	}
	return "", false
}

// replaceBounded replaces matches of re that are not glued to a letter or
// digit on either side (RE2's \b is ASCII-only, so Cyrillic needs this).
// repl returns false to keep a match unchanged.
func replaceBounded(re *regexp.Regexp, s string, repl func(m string) (string, bool)) string {
	locs := re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	last := 0
	for _, loc := range locs {
		st, en := loc[0], loc[1]
		if !boundaryBefore(s, st) || !boundaryAfter(s, en) {
			continue
		}
		r, ok := repl(s[st:en])
		if !ok {
			continue
		}
		b.WriteString(s[last:st])
		b.WriteString(r)
		last = en
	}
	b.WriteString(s[last:])
	return b.String()
}

func boundaryBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !isWordRune(r)
}

func boundaryAfter(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return !isWordRune(r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

func isLetterRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Mn, r)
}

// replaceNames replaces every letter run that is a form of a known name.
func replaceNames(s string, own, dict *nameIndex) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !isLetterRune(r) {
			b.WriteString(s[i : i+size])
			i += size
			continue
		}
		j := i
		for j < len(s) {
			r2, sz := utf8.DecodeRuneInString(s[j:])
			if !isLetterRune(r2) {
				break
			}
			j += sz
		}
		word := s[i:j]
		if !isLabelWord(s, i, j) && (own.matches(word) || (dict != nil && dict.matches(word))) {
			b.WriteString(LabelName)
		} else {
			b.WriteString(word)
		}
		i = j
	}
	return b.String()
}

// isLabelWord reports whether s[i:j] is the inside of a label such as [PHONE].
func isLabelWord(s string, i, j int) bool {
	if i == 0 || j >= len(s) || s[i-1] != '[' || s[j] != ']' {
		return false
	}
	switch "[" + s[i:j] + "]" {
	case LabelName, LabelPhone, LabelIIN, LabelEmail, LabelAddress, LabelDoc:
		return true
	}
	return false
}
