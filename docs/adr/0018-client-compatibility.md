# ADR-0018: Compatibility with installed app versions

Status: Accepted · Date: 2026-10-27

## Context
Mobile apps cannot be updated instantly; many users update months late (low storage, expensive data). A breaking API change or a forced update must never cut a mother off from crisis help.

## Decision
- **API evolution:** additive changes only within `/v1`; a breaking change is a new operation or `/v2`. Each released app version's contract is snapshotted in `contracts/openapi/released/`; CI checks breaking changes against every snapshot still in the support window, not only against `main`.
- **Support window:** the longer of 6 months or the last 3 released versions (configuration). Clients send `X-App-Version` and `X-App-Platform`; the server counts requests per version (no personal data) to decide when a version can be retired.
- **Updates:** a soft "update available" notice by default. A hard block (HTTP 426 with the minimum version) only for a security vulnerability or a version past the window. The block screen always shows crisis phone numbers and a working SOS fallback (call or SMS); the crisis endpoints stay reachable for blocked versions.
- **Remote feature flags:** a feature broken in an old client can be switched off server-side.
- **Offline queue:** requests queued by an older version and replayed by a newer one (SOS, crisis) are accepted in their original format, deduplicated by idempotency key.
- **OTA updates:** JavaScript-only updates from our own server implementing the open expo-updates protocol; updates are code-signed; delivered only to binaries with a matching runtime version (native fingerprint); staged rollout with rollback. Native changes ship through the stores with phased release.

## Consequences
Old contract snapshots and request-per-version metrics are part of the release process. The OTA signing key is held like other production keys (ADR-0009).
