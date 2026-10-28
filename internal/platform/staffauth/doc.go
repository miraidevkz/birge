// Package staffauth authenticates staff requests (/v1/staff/*, security
// "staffOidc" in the contract) with access tokens issued by the deployment's
// OIDC provider (ADR-0008 and its 2026-10-27 staff addendum). It is
// IdP-agnostic: Ory Hydra by default, Keycloak or any other provider that
// implements OAuth 2.0 token introspection (RFC 7662) through configuration.
//
// # Validation
//
// The middleware reads "Authorization: Bearer <token>" and asks the
// configured introspection endpoint about it, authenticating as a
// confidential client (HTTP Basic, RFC 6749 § 2.3.1). Introspection instead of
// local JWT verification keeps the backend free of a JWT library, works for
// opaque tokens (Hydra's default) and JWTs (Keycloak's) alike, and makes the
// provider the single authority on revocation. A token is accepted only when
// the response says "active": true and
//
//   - "iss" equals the configured issuer exactly (no trailing-slash
//     normalisation: the issuer is an identifier, not a URL to fetch);
//   - the configured audience is one of "aud" (string or array) or equals
//     "client_id" (Keycloak without an audience mapper puts only the admin
//     app's client there);
//   - "exp" is present and in the future (re-checked here because results
//     are cached);
//   - "token_use", when present (Hydra), is "access_token", and "typ", when
//     present (Keycloak), is "Bearer": refresh and ID tokens are refused;
//   - "sub" is present.
//
// # Claim contract (ADR-0008)
//
// Roles come from one claim path (Config.RolesClaim: "roles",
// "realm_access.roles", "ext.roles"), a string or an array of strings. Each
// value is translated by Config.RoleMap into one of our roles (psychologist,
// moderator, lawyer, admin); unknown values are ignored, so an IdP group that
// means nothing to Birge grants nothing. Authorization by role stays in the
// services (deny by default).
//
// MFA is required on every staff request. It is asserted when "acr" is one
// of Config.MFAACR or "amr" contains one of Config.MFAAMR. Both are read at
// the top level of the introspection response first and under "ext" second,
// because Hydra returns the session's access-token claims there. A valid
// token without MFA gets 403 "mfa_required" with an RFC 9470 step-up
// challenge in WWW-Authenticate.
//
// Staff ID (auth.Principal.IdentityID): "sub" when it is a UUID (Keycloak,
// and Hydra with Kratos as the login provider), otherwise a UUIDv5 of
// issuer and sub under a fixed namespace. Safety stores staff IDs as UUIDs
// (audit actor, duty shift), so the ID must be a UUID and stable for the same
// person across sign-ins; including the issuer keeps equal subjects of two
// providers apart. Changing the namespace or the derivation re-identifies
// every non-UUID staff member, so it must never change.
//
// # Staff accounts
//
// When Config.Accounts is set, a request whose token passed every check (MFA
// included) records the staff account through it (package staffdir: staff
// ID, display name, mapped roles, time seen), so the audit log can show names
// behind staff IDs. The display name is the "name" claim, else
// "preferred_username" (top level, then under "ext"), cleaned and trimmed to
// 80 characters; "email" is never read and a value containing "@" is dropped.
// The name is cached with the principal but never put into auth.Principal.
//
// Recording is throttled to once per staff ID per hour per process by an
// in-memory map with a bound, so the hot path is one map lookup. The write
// has its own short deadline and never fails the request: on error the
// middleware logs "StaffAccountRecordFailed" with the staff ID only and
// retries after a minute.
//
// # Caching and failure behaviour
//
// Results that pass the checks above are cached for at most CacheTTL
// (default and maximum 60s, and never past "exp") in a bounded LRU keyed by
// the SHA-256 of the token (package bearer). Consequence: a token revoked at
// the provider stays usable for up to CacheTTL on each replica; back-channel
// logout is a follow-up. Rejected tokens are not cached.
//
// The raw token is never stored, logged or sent anywhere except the
// introspection endpoint, in the request body; redirects are not followed so
// it cannot be forwarded to another host. Missing or invalid tokens get 401;
// an unreachable or failing introspection endpoint (including a rejection of
// our client credentials) gets 503 "auth_unavailable". When staff
// authentication is not configured, wiring answers every staff route with
// 503 "staff_auth_not_configured" (Unconfigured) and never falls back to
// member sessions.
package staffauth
