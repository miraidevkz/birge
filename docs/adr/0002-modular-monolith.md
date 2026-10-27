# ADR-0002: Modular monolith by bounded contexts

Status: Accepted · Date: 2026-10-27

## Context
The product has ~13 distinct domains, a small team, agent-driven development and a target of horizontal scaling on Kubernetes.

## Decision
One Go binary and one database, split into bounded contexts (`internal/domains/<context>`). Contexts own their tables and talk via service interfaces or domain events. Boundaries are enforced on every test run by `internal/archtest` (another context only through its `service` package; platform never imports domains).

## Alternatives
- Microservices per domain: network, deployment and consistency costs far above what the team and load need.
- Layered monolith (handlers/services/repos): no domain boundaries, agents step on each other.

## Consequences
A context can be extracted into a service later, because it already communicates through interfaces and events. Cross-context joins in SQL are forbidden.
