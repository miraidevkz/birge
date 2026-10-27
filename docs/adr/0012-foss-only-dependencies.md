# ADR-0012: FOSS-only dependencies

Status: Accepted · Date: 2026-10-27

## Context
The project is open source and must be deployable by anyone without paying a vendor.

## Decision
Only OSI-approved licenses. No open-core component whose paid features we need, and no SaaS on the critical path; hosted versions may exist only as optional choices. Every new runtime dependency is justified in its PR; versions are pinned.

## Consequences
Rejected so far: Atlas (open-core), HashiCorp Vault (BSL), paid mobile build services as a requirement.
