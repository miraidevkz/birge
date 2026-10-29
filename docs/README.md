# Birge documentation

Start with the part that matches what you came to do. Every page says at the top what it is for and whom.

## I want to understand what Birge is

- [Overview](overview.md): who uses Birge, what it does, the six rules it never breaks, and a picture of the system.
- [Scenarios](scenarios.md): what happens, step by step, when a mother signs in, fills in a check-in, presses SOS, talks to the assistant or deletes her account.
- [Glossary](glossary.md): the words the project uses (Circle, met in person, trusted contact, escalation, tombstone, …).
- [Roadmap](roadmap.md): what is built and what the pilot still needs.
- [MVP requirements](requirements/mvp.md): the product requirements the code is built against.

## I work as staff (psychologist, moderator, lawyer, admin)

- [Staff handbook](handbook/staff.md): duty shifts, escalations, SOS, moderation, members and rooms, the knowledge base, the audit log and metrics.

## I write texts for the apps

- [Content style](content-style.md): how Birge speaks to a tired mother: warm, no blame, always a next step.
- [Content review, October 2026](content-review-2026-10.md): the first tone pass, waiting for the psychologist and a native Kazakh speaker.

## I develop Birge

- [Development guide](development.md): set up, run locally, everyday commands, configuration, common changes, testing, CI.
- [Architecture](architecture.md): how the system is put together and why.
- [Architecture decision records](adr/README.md): every decision that is costly to reverse, with its reasons.
- [Solved problems](solved-problems.md): rules learned from real bugs; check new code against them.
- Each bounded context has its own `README.md` in `internal/domains/<context>/` (template: [templates/context-readme.md](templates/context-readme.md)); apps and packages have theirs in `apps/` and `packages/`.
- [Contributing](../CONTRIBUTING.md) and, for AI agents, [AGENTS.md](../AGENTS.md) with the [brief template](agents/brief-template.md).

## I deploy and operate Birge

- [Helm chart](../deploy/helm/birge/README.md): installing on k3s, secrets, values for the pilot.
- [Development guide § Configuration](development.md#configuration): every environment variable, demo presets that a real deployment must replace, and key rotation.
- [Development guide § Observability](development.md#observability) and [ADR-0021](adr/0021-observability.md): metrics, traces and alerts.

## I review security or privacy

- [Threat model](threat-model.md): trust boundaries, threats and controls (STRIDE).
- [Security policy](../SECURITY.md): how to report a vulnerability.
- Decisions behind the controls: [encryption](adr/0009-encryption.md), [data residency](adr/0013-data-residency.md), [member safety](adr/0019-user-safety-and-media-policy.md), [crisis path](adr/0023-crisis-path.md), [analytics](adr/0024-analytics.md).
