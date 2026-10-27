package staffauth

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Staff roles of the Birge claim contract (ADR-0008). Services authorize by
// these names; the IdP's own names are translated by Config.RoleMap.
const (
	RolePsychologist = "psychologist"
	RoleModerator    = "moderator"
	RoleLawyer       = "lawyer"
	RoleAdmin        = "admin"
)

// Roles lists every staff role of the claim contract.
var Roles = []string{RolePsychologist, RoleModerator, RoleLawyer, RoleAdmin}

// RoleIsValid reports whether r is a staff role of the claim contract.
func RoleIsValid(r string) bool { return slices.Contains(Roles, r) }

// DefaultRolesClaim is the claim path read when Config.RolesClaim is empty.
const DefaultRolesClaim = "roles"

// staffNamespace is the UUIDv5 namespace of staff IDs derived from a non-UUID
// subject. It is part of every such staff ID: never change it.
var staffNamespace = uuid.MustParse("3b0f7c52-8d0e-4f43-9a6b-6b1f0e6f2c1d")

// StaffID returns the staff ID for subject sub of issuer iss: sub itself when
// it is a UUID, otherwise UUIDv5(staffNamespace, iss + NUL + sub). The issuer
// is fixed per deployment, so the NUL separator keeps the name unambiguous.
func StaffID(iss, sub string) uuid.UUID {
	if id, err := uuid.Parse(sub); err == nil && len(sub) == 36 && id != uuid.Nil {
		return id
	}
	return uuid.NewSHA1(staffNamespace, []byte(iss+"\x00"+sub))
}

// claims is a decoded introspection response. Numbers stay json.Number so
// that "exp" is read without float rounding.
type claims map[string]any

// lookup follows a dotted path ("realm_access.roles") through nested objects.
func (c claims) lookup(path string) (any, bool) {
	var cur any = map[string]any(c)
	for _, seg := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[seg]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// str returns the string claim at path, or "" when it is absent or not a string.
func (c claims) str(path string) string {
	v, _ := c.lookup(path)
	s, _ := v.(string)
	return s
}

// list returns the claim at path as a list: a string is one value, an
// array keeps its string elements; anything else is empty.
func (c claims) list(path string) []string {
	v, ok := c.lookup(path)
	if !ok {
		return nil
	}
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// expiry returns the "exp" claim (seconds since the epoch).
func (c claims) expiry() (time.Time, bool) {
	n, ok := c["exp"].(json.Number)
	if !ok {
		return time.Time{}, false
	}
	if i, err := n.Int64(); err == nil {
		return time.Unix(i, 0), true
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f > math.MaxInt64 || f < 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

// mfaClaim returns the claim name at the top level, or under "ext" (Hydra)
// when the top level lacks it.
func (c claims) mfaClaim(name string) []string {
	if _, ok := c[name]; ok {
		return c.list(name)
	}
	return c.list("ext." + name)
}

// policy is the claim contract of one deployment: where roles live, how
// they translate, and which acr/amr values count as MFA.
type policy struct {
	issuer     string
	audience   string
	rolesClaim string
	roleMap    map[string]string
	mfaACR     []string
	mfaAMR     []string
}

// audienceOK reports whether the token was issued for us: the configured
// audience is in "aud" or is the "client_id".
func (p *policy) audienceOK(c claims) bool {
	return slices.Contains(c.list("aud"), p.audience) || c.str("client_id") == p.audience
}

// tokenKindOK refuses refresh and ID tokens where the IdP says what a token is.
func tokenKindOK(c claims) bool {
	if use, ok := c["token_use"]; ok && use != "access_token" {
		return false
	}
	if typ, ok := c["typ"]; ok {
		s, _ := typ.(string)
		return strings.EqualFold(s, "Bearer")
	}
	return true
}

// roles maps the IdP's role values to our roles: deduplicated, sorted,
// unknown values ignored.
func (p *policy) roles(c claims) []string {
	var out []string
	for _, v := range c.list(p.rolesClaim) {
		if r, ok := p.roleMap[v]; ok && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	slices.Sort(out)
	return out
}

// mfa reports whether the token asserts multi-factor authentication.
func (p *policy) mfa(c claims) bool {
	for _, acr := range c.mfaClaim("acr") {
		if slices.Contains(p.mfaACR, acr) {
			return true
		}
	}
	for _, amr := range c.mfaClaim("amr") {
		if slices.Contains(p.mfaAMR, amr) {
			return true
		}
	}
	return false
}
