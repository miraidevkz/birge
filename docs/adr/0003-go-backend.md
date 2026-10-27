# ADR-0003: Go for the backend

Status: Accepted · Date: 2026-10-27

## Context
Priorities: low resource use and latency, low token cost for agent-written code, fast builds, small containers, security.

## Decision
Go. Rust only for a hot path proven by profiling.

## Alternatives
- Rust: lower memory, stronger compile-time guarantees, but slower builds and more compile-fix iterations for agents; no official Anthropic SDK or Tink.
- Python/FastAPI, TypeScript/Node: higher resource use; larger dependency trees (supply-chain risk).

## Consequences
Two languages in the repository (Go, TypeScript); API types cross the boundary through generated code (ADR-0005).
