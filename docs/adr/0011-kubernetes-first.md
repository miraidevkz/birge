# ADR-0011: Kubernetes first, Compose as alternative

Status: Accepted · Date: 2026-10-27

## Context
The target is Kubernetes (possibly edge later); maintaining many containers with Compose does not scale.

## Decision
Helm charts in `deploy/helm` are the primary deployment; small installs run the same charts on single-node k3s. `deploy/compose` is kept for development. Services are stateless, have liveness/readiness probes, graceful shutdown, configuration from the environment, OpenTelemetry. Images are distroless. Edge nodes outside the jurisdiction may serve only public static content.

## Consequences
Operators: CloudNativePG for PostgreSQL, NATS chart, OpenBao chart. Backups go to S3-compatible storage inside the jurisdiction (SeaweedFS or Garage).
