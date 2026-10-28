package staffauth

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"birge/internal/platform/auth"
)

const (
	// accountRefresh is how often one staff account is recorded again by
	// one process: often enough for "last seen" and a changed name or role
	// set, rare enough that the hot path is a map lookup.
	accountRefresh = time.Hour
	// accountRetry is the wait after a failed record, so a database outage
	// costs one attempt per staff member per minute, not one per request.
	accountRetry = time.Minute
	// accountRecordTimeout bounds the one request per hour that records.
	accountRecordTimeout = 2 * time.Second
	// maxTrackedAccounts bounds the throttle map. A deployment has tens of
	// staff; the bound only matters against a flood of distinct valid
	// tokens, and overflowing it costs extra upserts, never correctness.
	maxTrackedAccounts = 10_000
	// MaxDisplayNameLen is the longest stored display name, in characters
	// (platform.staff_accounts and the contract's StaffAccount.displayName).
	MaxDisplayNameLen = 80
)

// Account is what the middleware records about a staff member whose token
// passed every check, MFA included.
type Account struct {
	StaffID uuid.UUID
	// DisplayName is the IdP "name" claim, else "preferred_username",
	// cleaned and trimmed to MaxDisplayNameLen; "" when neither is usable.
	// The email claim is never read, and a value that looks like an email
	// address is dropped.
	DisplayName string
	// Roles are the mapped staff roles (sorted, deduplicated).
	Roles  []string
	SeenAt time.Time
}

// AccountRecorder stores staff accounts (package staffdir implements it).
// It must be safe for concurrent use.
type AccountRecorder interface {
	RecordStaffAccount(ctx context.Context, a Account) error
}

// accountThrottle remembers, per staff ID, when the account may be recorded
// again. Claiming the slot before the write keeps concurrent requests of one
// staff member from writing together.
type accountThrottle struct {
	mu   sync.Mutex
	next map[uuid.UUID]time.Time
	max  int
}

func newAccountThrottle(limit int) *accountThrottle {
	return &accountThrottle{next: make(map[uuid.UUID]time.Time), max: limit}
}

// claim reports whether id is due at now and, when it is, reserves it until
// now + accountRefresh.
func (t *accountThrottle) claim(id uuid.UUID, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if next, ok := t.next[id]; ok && now.Before(next) {
		return false
	}
	if _, ok := t.next[id]; !ok && len(t.next) >= t.max {
		t.prune(now)
	}
	t.next[id] = now.Add(accountRefresh)
	return true
}

// retryAt moves id's next slot to at (after a failed write).
func (t *accountThrottle) retryAt(id uuid.UUID, at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.next[id] = at
}

// prune drops expired slots; when the map is still full it starts over. Called
// with mu held, only when the map is full.
func (t *accountThrottle) prune(now time.Time) {
	for id, next := range t.next {
		if !now.Before(next) {
			delete(t.next, id)
		}
	}
	if len(t.next) >= t.max {
		clear(t.next)
	}
}

// recordAccount records p's account when its hourly slot is due. It never
// fails the request: a failed write is logged with the event name and staff
// ID only (no name, no error text that could quote a value) and retried after
// accountRetry. The write gets its own deadline and survives a client that
// hangs up, so a slow database delays this one request by at most
// accountRecordTimeout.
func (m *Middleware) recordAccount(ctx context.Context, p auth.Principal, name string) {
	now := m.clock.Now()
	if !m.throttle.claim(p.IdentityID, now) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), accountRecordTimeout)
	defer cancel()
	err := m.accounts.RecordStaffAccount(ctx, Account{
		StaffID: p.IdentityID, DisplayName: name, Roles: append([]string(nil), p.Roles...), SeenAt: now,
	})
	if err != nil {
		m.throttle.retryAt(p.IdentityID, now.Add(accountRetry))
		m.logger.Warn("staff account not recorded", "event", "StaffAccountRecordFailed", "staff_id", p.IdentityID)
	}
}

// displayName picks the IdP's display name: "name", else
// "preferred_username", each at the top level of the introspection response
// first and under "ext" (Hydra) second. "email" is never read.
func displayName(c claims) string {
	for _, claim := range []string{"name", "preferred_username"} {
		v := c.str(claim)
		if v == "" {
			v = c.str("ext." + claim)
		}
		if v = cleanDisplayName(v); v != "" {
			return v
		}
	}
	return ""
}

// cleanDisplayName makes an IdP value safe to store and show: valid UTF-8,
// no control characters, whitespace collapsed, at most MaxDisplayNameLen
// characters. A value containing "@" is treated as an email address (Keycloak
// often uses the email as the username) and dropped: emails are not stored.
func cleanDisplayName(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if strings.Contains(s, "@") {
		return ""
	}
	if utf8.RuneCountInString(s) > MaxDisplayNameLen {
		s = strings.TrimSpace(string([]rune(s)[:MaxDisplayNameLen]))
	}
	return s
}
