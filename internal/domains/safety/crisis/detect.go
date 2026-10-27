package crisis

import (
	"sort"
	"strings"
)

// Result is the outcome of Detect. It never contains the message text.
type Result struct {
	// Crisis is true when any lexicon entry matched.
	Crisis bool
	// Category is the lexicon category of the first match (file name part,
	// e.g. "suicide"); empty when Crisis is false.
	Category string
	// Lang is the language of the lexicon that matched; empty when Crisis is false.
	Lang string
}

// Detect reports whether text matches the lexicon. lang is the member's
// language ("kk", "ru" or "en"; anything else is allowed) and is only the order in
// which languages are checked: every loaded language is checked (see the
// package documentation). Detect is pure and safe for concurrent use; a nil
// Lexicon matches nothing.
func (l *Lexicon) Detect(text, lang string) Result {
	if l == nil || l.size == 0 || strings.TrimSpace(text) == "" {
		return Result{}
	}
	n := Normalize(text)
	if len(n.Words) == 0 {
		return Result{}
	}
	words := make([][]rune, len(n.Words))
	for i, w := range n.Words {
		words[i] = []rune(w)
	}
	for _, lg := range l.order(lang) {
		for _, cat := range l.langs[lg] {
			if cat.matches(n, words) {
				return Result{Crisis: true, Category: cat.name, Lang: lg}
			}
		}
	}
	return Result{}
}

// order returns lang first (when loaded), then the other languages sorted.
func (l *Lexicon) order(lang string) []string {
	out := make([]string, 0, len(l.langs))
	if _, ok := l.langs[lang]; ok {
		out = append(out, lang)
	}
	rest := make([]string, 0, len(l.langs))
	for lg := range l.langs {
		if lg != lang {
			rest = append(rest, lg)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func (c *category) matches(n Normalized, words [][]rune) bool {
	for i := range c.phrases {
		if c.phrases[i].matches(n, words) {
			return true
		}
	}
	for _, re := range c.regexps {
		if re.MatchString(n.Spaced) {
			return true
		}
	}
	return false
}

func (p *phrase) matches(n Normalized, words [][]rune) bool {
	if p.compact != "" && strings.Contains(n.Compact, p.compact) {
		return true
	}
	last := len(words) - len(p.words)
	for i := 0; i <= last; i++ {
		ok := true
		for j := range p.words {
			if !p.words[j].matches(words[i+j]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func (w *word) matches(in []rune) bool {
	if w.prefix {
		if hasPrefix(in, w.runes) {
			return true
		}
	} else if equal(in, w.runes) {
		return true
	}
	return len(w.runes) >= FuzzyMinLen && withinOne(w.runes, in, w.prefix)
}

// withinOne reports whether in is at most one edit away from pat (insertion,
// deletion, substitution or adjacent transposition: optimal string alignment
// distance <= 1). With prefix, it reports whether some prefix of in is. It
// walks both words once and allocates nothing, because it runs for every
// word of every message against every long phrase word.
func withinOne(pat, in []rune, prefix bool) bool {
	rest := func(p, s []rune) bool { // p matches s exactly (or as a prefix of s)
		if prefix {
			return hasPrefix(s, p)
		}
		return equal(s, p)
	}
	for i := 0; i < len(pat); i++ {
		if i >= len(in) {
			return len(pat)-i == 1 // only the last pattern letter is missing
		}
		if pat[i] == in[i] {
			continue
		}
		switch {
		case rest(pat[i+1:], in[i+1:]): // substitution
		case rest(pat[i+1:], in[i:]): // letter missing from in
		case rest(pat[i:], in[i+1:]): // extra letter in in
		case i+1 < len(pat) && i+1 < len(in) && pat[i] == in[i+1] && pat[i+1] == in[i] &&
			rest(pat[i+2:], in[i+2:]): // transposition
		default:
			return false
		}
		return true
	}
	// pat is a prefix of in: exact for prefix mode, else at most one extra letter.
	return prefix || len(in)-len(pat) <= 1
}

func hasPrefix(s, prefix []rune) bool {
	return len(s) >= len(prefix) && equal(s[:len(prefix)], prefix)
}

func equal(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
