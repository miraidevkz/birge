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
