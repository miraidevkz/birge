# ADR-0021: Self-hosted observability without personal data

Status: Accepted · Date: 2026-10-27

## Context
Operators need metrics, logs, traces, errors and crash reports. Third-party SaaS would move data out of the jurisdiction.

## Decision
- Instrumentation with OpenTelemetry. Metrics: Prometheus; logs: Loki; traces: Tempo; dashboards: Grafana; alerts: Alertmanager. All self-hosted, in-jurisdiction.
- Errors and crash reports from the backend and the apps: GlitchTip (self-hosted, Sentry-protocol compatible) with the open-source Sentry SDKs; personal data is scrubbed in the SDK before sending.
- Alerts are delivered through our own `notify` module (push, SMS) via an Alertmanager webhook, so on-call uses the same escalation ladder as the product.
- Logs carry IDs, event names and error codes only; never message text, request bodies or personal data. Web server logs mask IP addresses or have a short retention.
- Required alerts: service down (also from an external probe), LLM error rate, untaken escalation older than 15 minutes, oldest overdue timer, outbox lag, SMS send spike (anti-pumping), backup and restore-drill failures.

## Consequences
Observability components are part of the Helm charts and of the restore drills.
