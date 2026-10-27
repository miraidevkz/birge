# ADR-0022: Knowledge base editing and review

Status: Accepted · Date: 2026-10-27

## Context
The knowledge base is written by a psychologist and a lawyer who will not use git. The AI may only cite reviewed content. Laws change, so a reviewed legal answer can become wrong.

## Decision
- **Editing in the staff admin:** draft → review → published, versioned, with an audit trail. Only a reviewer with the matching role (psychologist for self-help, lawyer for legal) can publish; the reviewer and date are recorded on the version.
- **Languages:** each language version (kk, ru, en) is reviewed and published on its own; the Kazakh version is reviewed with a native speaker. Versions are linked; editing one marks the others "needs sync". If no reviewed version exists in the member's language, the AI says it does not know and offers a specialist.
- **Expiry:** legal content expires 6 months after review, self-help content after 12 months (country configuration). The reviewer gets a task a month before expiry. Expired legal content is shown with a "being re-checked" notice and is excluded from AI retrieval. A background job checks source links.
- **Index:** publishing or expiry updates the retrieval index (ADR-0010) through an event; only published, unexpired versions are indexed.
- **Repository export:** published content is exported to `knowledge/{kk,ru,en}/` as markdown with front matter (`topic`, `source_url`, `reviewed_by`, `reviewed_at`, `expires_at`), so other deployments can reuse it and AI evaluations (ADR-0017) run against it. Drafts never leave the admin.

## Consequences
The `knowledge` context owns documents, versions, reviews and expiry timers (ADR-0020). Reviewer workload is visible in the admin.
