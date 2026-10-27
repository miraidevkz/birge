package crisis

import (
	"strings"
	"unicode"
)

// Normalized is a message after normalisation.
type Normalized struct {
	// Words are the normalised words in order.
	Words []string
	// Spaced is " " + Words joined by single spaces + " "; regular
	// expressions run against it.
	Spaced string
	// Compact is Words concatenated without separators, with repeated
	// letters collapsed again across the joins.
	Compact string
}

// upperLookAlikes maps upper-case Latin letters to the Cyrillic letters they
// look like. Applied before case folding because B/H/M/T only resemble
// Cyrillic in upper case.
var upperLookAlikes = map[rune]rune{
	'A': 'а', 'B': 'в', 'C': 'с', 'E': 'е', 'H': 'н', 'K': 'к', 'M': 'м',
	'O': 'о', 'P': 'р', 'T': 'т', 'X': 'х', 'Y': 'у',
}

// lowerLookAlikes maps lower-case Latin letters and digits to Cyrillic
// look-alikes used in chat ("4то", "y", "u" for "и", "n" for "п").
var lowerLookAlikes = map[rune]rune{
	'a': 'а', 'b': 'ь', 'c': 'с', 'e': 'е', 'h': 'н', 'i': 'и', 'k': 'к',
	'm': 'м', 'n': 'п', 'o': 'о', 'p': 'р', 'r': 'г', 't': 'т', 'u': 'и',
	'x': 'х', 'y': 'у', 'ë': 'е', 'ı': 'и',
	'0': 'о', '3': 'з', '4': 'ч', '6': 'б',
}

// variants folds Kazakh letters and Russian spelling variants into one
// reduced alphabet; 0 means the letter is dropped.
var variants = map[rune]rune{
	'ә': 'а', 'ғ': 'г', 'қ': 'к', 'ң': 'н', 'ө': 'о', 'ұ': 'у', 'ү': 'у',
	'һ': 'х', 'і': 'и', 'ї': 'и',
	'ё': 'е', 'й': 'и', 'э': 'е', 'щ': 'ш',
	'ь': 0, 'ъ': 0,
}

func isCyrillic(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }

func isLookAlike(r rune) bool {
	if _, ok := upperLookAlikes[r]; ok {
		return true
	}
	_, ok := lowerLookAlikes[unicode.ToLower(r)]
	return ok
}

// Normalize reduces text as described in the package documentation. It is
// exported so the lexicon loader, the evaluation tooling and tests use the
// exact same transformation as Detect.
func Normalize(text string) Normalized {
	chunks := strings.Fields(text)
	msgCyrillic := strings.IndexFunc(text, isCyrillic) >= 0

	words := make([]string, 0, len(chunks))
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			words = append(words, collapse(b.String()))
			b.Reset()
		}
	}
	for _, chunk := range chunks {
		mapLatin := strings.IndexFunc(chunk, isCyrillic) >= 0 ||
			(msgCyrillic && allLookAlikes(chunk))
		for _, r := range chunk {
			if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Cf, r) {
				continue // combining marks, zero-width and soft-hyphen characters
			}
			if mapLatin {
				if c, ok := upperLookAlikes[r]; ok {
					r = c
				} else if c, ok := lowerLookAlikes[unicode.ToLower(r)]; ok {
					r = c
				}
			}
			r = unicode.ToLower(r)
			if v, ok := variants[r]; ok {
				if v == 0 {
					continue
				}
				r = v
			}
			if !unicode.IsLetter(r) {
				flush()
				continue
			}
			b.WriteRune(r)
		}
		flush()
	}
	compact := collapse(strings.Join(words, ""))
	spaced := " " + strings.Join(words, " ") + " "
	return Normalized{Words: words, Spaced: spaced, Compact: compact}
}

// allLookAlikes reports whether every letter or digit of chunk has a
// Cyrillic look-alike and there is at least one.
func allLookAlikes(chunk string) bool {
	n := 0
	for _, r := range chunk {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			continue
		}
		if !isLookAlike(r) {
			return false
		}
		n++
	}
	return n > 0
}

// collapse replaces every run of the same rune with one rune.
func collapse(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev rune = -1
	for _, r := range s {
		if r == prev {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}
