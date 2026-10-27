// Package auth carries the authenticated principal through the request context.
// The session middleware (members) and the staffauth middleware (staff,
// /v1/staff/*) set it (ADR-0008); services read it.
package auth

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey struct{}

// Principal is the authenticated caller.
type Principal struct {
	// IdentityID is the identity provider's ID (Kratos identity for members,
	// OIDC subject mapped to a UUID for staff, see staffauth.StaffID). For a
	// member only membership uses it, to find or create the member; for staff
	// it is the staff ID (audit actor, duty shifts).
	IdentityID uuid.UUID
	// MemberID is the membership member ID of a member session, set by the
	// member-resolution middleware before any other context's handler runs.
	// uuid.Nil for staff and anonymous requests. Contexts other than
	// membership key their data by MemberID, never by IdentityID.
	MemberID uuid.UUID
	// Staff is true for staff sessions from the admin.
	Staff bool
	// Roles are staff roles from the claim mapping; empty for members.
	Roles []string
	// MFA is true when the identity provider asserted multi-factor authentication.
	MFA bool
}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal, or false for anonymous requests.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}
