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
