# Roadmap

What is built, what the pilot still needs, and what comes after. Status as of 2026-10-29 (release v0.1.0).

Contracts land before code ([ADR-0005](adr/0005-contracts-first.md)): an operation whose code is not written yet answers `501 Not Implemented`, so clients can be built against the contract first. Today every operation in the contract is implemented.

## Built

- **Backend.** One Go binary, `birge`, with the subcommands `api`, `worker`, `migrate`, `keygen`, `kb-reindex` and `kb-export`. Fourteen bounded contexts: membership, notify, safety, assistant, knowledge, wellbeing, circle, chat, emergency, sharing, directory, moderation, privacy and analytics. Shared platform packages cover the database, outbox and NATS JetStream, timers, encryption with Tink, the audit log, staff sign-in, request validation, object storage and rate limits.
- **Sign-in and delivery.** An auth gateway in front of Ory Kratos (proof of work, number allowlist, rate limits, daily SMS budget, Kratos courier hook). Push through FCM and APNs and SMS through SMSC.kz, with SMS as the fallback for critical alerts; production refuses the log-only senders. Several API replicas share rate limits and SMS guards through PostgreSQL.
- **Safety and AI.** Crisis detection by lexicon before any model call, the fixed crisis response, escalation to the on-duty psychologist with a backup after 15 minutes, the AI assistant with anonymization and retrieval, and a knowledge base with expert review and expiry.
- **Product.** Weekly check-ins, Circle and chats with moderation, SOS for moderators and trusted contacts, the emergency plan with a one-time link for external contacts, sharing and the specialist directory, consents and account deletion in every context, and pilot metrics with CSV export ([ADR-0024](adr/0024-analytics.md)).
- **Clients.** The mom app for Android, iOS and the web in Kazakh, Russian and English, including the notification inbox, trusted-contact screens and the external plan page. The staff admin for escalations, SOS, moderation, members, rooms and mentors, duty roster, knowledge base, metrics and the audit log.
- **Quality.** The offline AI evaluation (`cmd/birge-eval`) on every change, a per-context coverage ratchet, vulnerability scanning, a contract diff against released app versions, workflow lint, and a release image with SBOM and signature.
- **Operations.** A Helm chart for k3s, OpenTelemetry traces, Prometheus metrics and the required alerts ([ADR-0021](adr/0021-observability.md)), and a Docker Compose stack for development with demo content.
- **Documentation for people.** Overview, end-to-end scenarios, staff handbook and the content style guide.

## Still needed for the pilot

Backend:

- [ ] Record the concrete staff role (moderator, psychologist, …) in membership and chat audit rows; today they say `staff`.
- [ ] A re-wrap job for the keys of members who stay inactive after a key rotation, so the old key can be retired without waiting for them. Today a key is re-wrapped when its member is next active.
- [ ] A role matrix for staff and a deny-by-default test generated from the contract.
- [ ] Chat reports the direct room it opens for an accepted tea match back to circle, so `Match.roomId` is filled as the contract promises (a contract change first; today the member finds the room in her chat list).
- [ ] The monitoring alert when the next 24 hours of psychologist duty are not covered ([ADR-0023](adr/0023-crisis-path.md)); until then staff check the roster by hand.
- [ ] An isolation item in the weekly check-in, so the isolation metric has data. This is a product and psychologist decision first.

Quality and security:

- [ ] The performance budget of the mom app on a low-end Android phone: cold start, scrolling, memory ([ADR-0004](adr/0004-react-native-clients.md)).
- [ ] OWASP ASVS checklist, threat-model review, and a load test of the API and the chat gateway.
- [ ] Independent security audit.

Deployment:

- [ ] Production server in Kazakhstan, the SMS sender ID registered with the provider, and a Firebase project for Android push.
- [ ] Backup-and-restore drill and key-recovery drill.
- [ ] Store builds with a phased release, and a release keystore for Android.

Content:

- [ ] Review of all texts by a native Kazakh speaker and a psychologist ([content review](content-review-2026-10.md)).
- [ ] The crisis lexicon, questionnaire thresholds and crisis phone numbers approved by the psychologist and the operator. The repository ships demo values only.

Acceptance:

- [ ] The acceptance checklist ([requirements § 10](requirements/mvp.md)) and the pilot itself.

## After the pilot

- [ ] A documentation site built from `docs/` with mdBook (MPL-2.0), translated into Kazakh, Russian and English with mdbook-i18n-helpers (Apache-2.0), using the same Weblate workflow as the app strings.
