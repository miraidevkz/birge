package model

import (
	"regexp"
	"strings"
	"unicode"
)

// The content filters hold messages for moderation (ADR-0016: scams and
// fundraising usually come with a link or a phone number). They err on the
// side of holding: a false positive costs a moderator a click, a miss can
// cost a mother money. Obfuscated numbers written as words are not caught.

// linkRe matches URLs, "www." hosts, Telegram-style handles of messengers
// and bare domains with a common top-level domain.
var linkRe = regexp.MustCompile(`(?i)(https?://|www\.|t\.me/|wa\.me/|\b[\p{L}\p{N}][\p{L}\p{N}-]*\.(com|net|org|info|biz|io|me|ru|kz|рф|su|ua|uz|kg|online|site|shop|store|link|xyz|top|pro|app|club|ly|gl|cc)\b)`)

// minPhoneDigits is the shortest digit run treated as a phone number: a
// Kazakhstan number has 10 digits without the country code.
const minPhoneDigits = 10

// ContainsLink reports whether text contains a link.
func ContainsLink(text string) bool { return linkRe.MatchString(text) }

// ContainsPhone reports whether text contains a phone-like run: at least
// minPhoneDigits digits, separated by at most two spaces, dashes, dots or
// brackets in a row (as in "+7 (700) 000-00-00"), with an optional leading
// "+". Unicode digits of other
// scripts count too.
func ContainsPhone(text string) bool {
	digits, gap := 0, 0
	for _, r := range text {
		switch {
		case unicode.IsDigit(r):
			digits++
			gap = 0
			if digits >= minPhoneDigits {
				return true
			}
		case strings.ContainsRune(" -.()\u00a0", r) && digits > 0 && gap < 2:
			gap++
		case r == '+' && digits == 0:
		default:
			digits, gap = 0, 0
		}
	}
	return false
}

// HoldFor returns the filter hold reason for text, or "" when the filters
// pass it. The crisis check runs before and is not part of this.
func HoldFor(text string) string {
	switch {
	case ContainsLink(text):
		return HoldLink
	case ContainsPhone(text):
		return HoldPhone
	}
	return ""
}
