---
name: api-contract
description: Add or change an HTTP endpoint or a domain event in Birge. Use before writing handler or client code, and when reviewing contract changes in contracts/.
---

# Contract change

Contracts come before code (ADR-0005). The contract PR is separate from the implementation PR.

## HTTP (OpenAPI 3.1, `contracts/openapi/`)

1. Edit the spec. Every operation has an `operationId`, a tag equal to the bounded context, request/response schemas, error responses and a security requirement (or an explicit `security: []` with a reason).
2. Validate inputs in the schema: lengths, formats, enums, `additionalProperties: false`.
3. Never put personal data in paths or query strings; use the body.
4. Each tag (bounded context) has `contracts/codegen/<tag>.yaml` and a line in `API_AREAS` in the Makefile; run `make api-gen` (oapi-codegen strict server into `internal/domains/<context>/api/api.gen.go`). `make api-check` fails on stale code. Errors use `internal/platform/httpapi` problem handlers.
5. A breaking change needs a new version of the operation; mobile apps in the wild still use the old one. CI also diffs against every snapshot in `contracts/openapi/released/` within the support window (ADR-0018).
6. Crisis and SOS endpoints must keep accepting requests from blocked app versions and old offline-queue payloads.

## Events (JSON Schema, `contracts/events/`)

1. One schema per event: `<context>.<EventName>.v<N>.json`.
2. Events carry IDs and facts, never message text or decrypted personal data.
3. Required envelope fields: `id` (UUID, used for deduplication), `type`, `occurred_at`, `version`.
4. Changing the meaning or removing a field means a new version; consumers handle both until the old one is retired.

## Generated code

Never edit generated files. If they are wrong, fix the spec or the generator config.
