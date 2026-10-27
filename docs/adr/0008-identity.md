# ADR-0008: Identity: Ory, OIDC-agnostic

Status: Accepted · Date: 2026-10-27

## Context
Members sign in with a phone number and an SMS code from native apps. Staff need strong authentication and SSO. Resource use matters.

## Decision
- Members: Ory Kratos (headless, native flows, SMS code via a webhook to our SMS module with anti-pumping controls).
- Staff: Ory Hydra as the OIDC provider, WebAuthn required. The admin is reachable only through VPN.
- The backend accepts any OIDC IdP: roles come from our own claim contract with a mapping layer per IdP; MFA is verified via `acr`/`amr`; back-channel logout for immediate revocation. CI tests login against at least two IdPs (Hydra, Keycloak).

## Consequences
Keycloak or another IdP can be plugged in by configuration. To verify: which Ory features are under the enterprise license.

## Addendum (2026-10-27): sign-in path and sessions

- **Auth gateway** (`internal/domains/membership/authgw`, mounted under `/auth/` in front of the Kratos public API): verifies the proof-of-work challenge, allows only the configured mobile number ranges, applies per-number, per-IP and global rate limits and the daily SMS budget, then proxies to Kratos. Kratos is never exposed directly.
- **SMS codes:** the Kratos courier calls a cluster-internal webhook of `notify`, which sends through the SMS provider plugins with the same anti-pumping checks (defence in depth).
- **Sessions:** the API accepts the Kratos session token as a bearer token. A platform middleware validates it against Kratos (`whoami`) with a short cache and puts the identity ID in the request context.
- **Member records** are created lazily: the first authenticated request of a new identity creates the member (`membership.MemberRegistered`). No registration webhook.

## Addendum (2026-10-27): staff tokens

- **Validation by introspection.** Staff routes (`/v1/staff/*`) accept the provider's access token as a bearer token and check it with OAuth 2.0 token introspection (RFC 7662) as a confidential client (`internal/platform/staffauth`). This needs no JWT library, works for opaque (Hydra) and JWT (Keycloak) tokens, and leaves revocation to the provider. Accepted only with `active: true`, the configured issuer (exact match), the configured audience in `aud` or as `client_id`, a future `exp`, an access token (not refresh or ID), and a `sub`. Active results are cached for at most 60 s (never past `exp`) keyed by a hash of the token, so revocation takes effect within that time on each replica.
- **Claim mapping.** Configuration names one claim path for roles (`roles`, `realm_access.roles`, `ext.roles`) and a map from IdP values to our roles (`psychologist`, `moderator`, `lawyer`, `admin`); unknown values grant nothing. Authorization by role stays in the services.
- **MFA rule.** Every staff request needs MFA: `acr` in a configured list or `amr` containing a configured value (e.g. `webauthn`, `hwk`, `mfa`). A valid token without MFA gets 403 `mfa_required` with an RFC 9470 step-up challenge.
- **Staff ID.** `sub` when it is a UUID, otherwise a UUIDv5 of issuer and `sub`: safety stores staff IDs as UUIDs, and the derivation is stable per provider.
- **Separation.** Member sessions never open staff routes and staff tokens never open member routes; without a configured provider staff routes answer 503.
- **Follow-up:** back-channel logout for immediate revocation (until then bounded by the cache TTL); a CI login test against real Hydra and Keycloak (today emulated by both response shapes in tests).
