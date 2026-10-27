package anonymize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Russian endings after a name that ends in a consonant (Арман → Армана,
// Арманом; Иванов → Иванова, Ивановым) and after the stem of a name ending in
// -а/-я (Анна → Анны, Анне, Анну, Анной; possessive Анин, Анина).
var (
	ruAfterConsonant = []string{
		"а", "у", "ом", "ым", "ем", "е", "ы", "и", "ов", "ев", "ам", "ами", "ах",
		"ой", "ою", "ую", "ей", "ых", "ыми", "ин", "ина", "ину", "иной", "ины",
	}
	ruAfterA = []string{
		"а", "я", "ы", "и", "е", "у", "ю", "ой", "ей", "ою", "ею", "ам", "ям", "ами", "ями",
		"ах", "ях", "ин", "ина", "ину", "иной", "ины", "ино", "ином", "иных", "ын", "ына", "ыну", "ыной",
	}
	ruAfterJ    = []string{"й", "я", "ю", "ем", "е", "и", "ям", "ями", "ях", "ев"}
	ruAfterSoft = []string{"ь", "и", "ью", "я", "ю", "ем", "е", "ей"}

	// Kazakh case endings (vowel harmony and voicing variants), already
	// folded to the Russian-keyboard alphabet (see fold).
	kkCase = []string{
		"нын", "нин", "дын", "дин", "тын", "тин", // genitive
		"га", "ге", "ка", "ке", "на", "не", "а", "е", // dative
		"ны", "ни", "ды", "ди", "ты", "ти", "н", // accusative
		"да", "де", "та", "те", "нда", "нде", // locative
		"дан", "ден", "тан", "тен", "нан", "нен", // ablative
		"мен", "бен", "пен", "менен", // instrumental
		"лар", "лер", "дар", "дер", "тар", "тер", // plural
		"жан", "ке", "еке", // affectionate forms
	}
	kkPossessive = []string{"ым", "им", "м", "ын", "ин", "н", "сы", "си", "ы", "и", "ымыз", "имиз"}

	ruA, ruC, ruJ, ruSoft, kkAll map[string]bool
)

func init() {
	ruA = setOf(ruAfterA)
	ruC = setOf(ruAfterConsonant)
	ruJ = setOf(ruAfterJ)
	ruSoft = setOf(ruAfterSoft)
	kkAll = setOf(kkCase)
	for _, p := range kkPossessive {
		kkAll[p] = true
		for _, c := range kkCase {
			kkAll[p+c] = true
		}
	}
}

func setOf(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// nameIndex holds folded names and their declension stems.
type nameIndex struct {
	full  map[string]bool            // whole names (+ "" or a Kazakh suffix chain, or Russian consonant endings)
	stems map[string]map[string]bool // stem → allowed non-empty Russian endings
}

func newNameIndex() nameIndex {
	return nameIndex{full: map[string]bool{}, stems: map[string]map[string]bool{}}
}

// add indexes name when it has at least minLen letters.
func (x *nameIndex) add(name string, minLen int) {
	n := fold(strings.TrimSpace(name))
	if utf8.RuneCountInString(n) < minLen || strings.IndexFunc(n, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0 {
		return
	}
	x.full[n] = true
	last, size := utf8.DecodeLastRuneInString(n)
	stem := n[:len(n)-size]
	if utf8.RuneCountInString(stem) < 3 {
		return
	}
	var endings map[string]bool
	switch last {
	case 'а', 'я', 'a':
		endings = ruA
	case 'й':
		endings = ruJ
	case 'ь':
		endings = ruSoft
	}
	if endings != nil {
		x.stems[stem] = endings
	}
}

// matches reports whether word is a form of an indexed name.
func (x *nameIndex) matches(word string) bool {
	if len(x.full) == 0 {
		return false
	}
	w := fold(word)
	if x.full[w] {
		return true
	}
	// Try every split w = prefix + ending with a prefix of at least two letters.
	for i := range w {
		if i == 0 || utf8.RuneCountInString(w[:i]) < 2 {
			continue
		}
		prefix, ending := w[:i], w[i:]
		if x.full[prefix] {
			if kkAll[ending] {
				return true
			}
			if last, _ := utf8.DecodeLastRuneInString(prefix); !isVowel(last) && ruC[ending] {
				return true
			}
		}
		if endings, ok := x.stems[prefix]; ok && endings[ending] {
			return true
		}
	}
	return false
}

func isVowel(r rune) bool {
	return strings.ContainsRune("аеиоуыэюяaeiouy", r)
}

// fold lowercases and maps the letters members type interchangeably onto
// one alphabet: ё→е, Kazakh letters to their Russian-keyboard look-alikes,
// decomposed й/ё composed, other combining marks dropped.
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev rune = -1
	flush := func() {
		if prev >= 0 {
			b.WriteRune(prev)
		}
	}
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			switch {
			case r == '̆' && prev == 'и':
				prev = 'й'
			case r == '̈' && prev == 'е':
				prev = 'е'
			}
			continue
		}
		flush()
		r = unicode.ToLower(r)
		switch r {
		case 'ё':
			r = 'е'
		case 'ә':
			r = 'а'
		case 'ғ':
			r = 'г'
		case 'қ':
			r = 'к'
		case 'ң':
			r = 'н'
		case 'ө':
			r = 'о'
		case 'ұ', 'ү':
			r = 'у'
		case 'һ':
			r = 'х'
		case 'і':
			r = 'и'
		}
		prev = r
	}
	flush()
	return b.String()
}
