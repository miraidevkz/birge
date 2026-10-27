# ADR-0005: Contracts before code

Status: Accepted · Date: 2026-10-27

## Context
Modules are built in parallel by different agents.

## Decision
HTTP API in OpenAPI 3.1 (`contracts/openapi`), events in JSON Schema (`contracts/events`). Contracts change first, in their own PR; Go server stubs and the TypeScript client are generated. CI fails on breaking changes against `main` and on stale generated code.

## Consequences
An agent can build a module from its contract alone. Contract review becomes the main integration point.
