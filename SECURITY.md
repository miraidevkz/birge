# Security policy

Birge handles sensitive data about vulnerable people: mental-health check-ins, crisis messages, children's special needs and emergency plans. We treat security reports as a priority.

## Reporting a vulnerability

Please **do not** open a public issue, pull request or discussion for a security problem.

Email **developers@miraidev.kz** with the subject `SECURITY: <short title>` and include:

- what is affected (component, endpoint, app version, commit);
- steps to reproduce or a proof of concept;
- the impact you expect;
- whether you want to be credited, and under which name.

Do not include real personal data in the report. If you came across someone's personal data while testing, stop, tell us, and delete your copy.

## What to expect

| Step | Target |
|---|---|
| Acknowledgement | 3 business days |
| Initial assessment and severity | 7 days |
| Fix for critical/high issues | as fast as possible, normally within 30 days |
| Coordinated disclosure | after a fix is released, or 90 days after the report, whichever comes first, unless we agree otherwise |

## Scope

In scope: this repository (backend, apps, deployment manifests, CI), and any deployment we operate.

Out of scope: denial-of-service and volumetric attacks, social engineering of staff or users, physical attacks, findings that require a rooted/jailbroken device with no further impact, missing best-practice headers with no demonstrated impact, and third-party services we do not control.

## Safe harbour

We will not pursue legal action against good-faith research that follows this policy: avoid privacy violations and service disruption, use only test accounts, do not access or modify other people's data beyond what is needed to demonstrate the issue, and give us reasonable time to fix it before disclosure.

## Supported versions

The backend `main` branch and the mobile app versions inside the support window ([ADR-0018](docs/adr/0018-client-compatibility.md)) receive security fixes.

## Security design

See [`docs/threat-model.md`](docs/threat-model.md) and the ADRs in [`docs/adr/`](docs/adr/).
