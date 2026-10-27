# Threat model

Status: wave 0 baseline · Method: STRIDE per trust boundary and critical flow · Review: on every ADR that changes a boundary, and before each release.

## Assets

| Asset | Why it matters | Classification |
|---|---|---|
| Crisis messages, escalations | life safety; extremely sensitive | critical |
| Child cards (emergency plan), children's needs | health data of minors | critical |
| Check-in answers and scores | mental-health data | critical |
| Identity: phone, name, district | re-identification, physical safety (domestic violence) | high |
| Chat messages, relay messages | private conversations | high |
| Acquaintance graph, trusted contacts, blocks | who knows whom; who avoids whom | high |
| Knowledge base (published) | wrong legal/medical guidance harms users | high (integrity) |
| Keys: master key, per-user keys, OTA signing key, IdP keys | compromise = data or code-supply compromise | critical |
| Audit log | accountability of staff access | high (integrity) |
| Service availability: crisis path, SOS | delays can cost lives | critical (availability) |

## Actors

| Actor | Capability | Motivation |
|---|---|---|
| Curious or abusive member | valid account, app UI and API | stalking, harassment, scams, finding a person |
| Abusive family member | physical access to the mother's phone | control, finding out what she shares |
| Fake member | registers with a bought number | access to moms and children, scams, fundraising fraud |
| Automated attacker | scripts against public endpoints | SMS pumping, credential stuffing, scraping |
| Malicious or careless staff member | admin access via VPN | snooping on personal data, leaks |
| Compromised dependency or agent | code execution in CI or dev | supply-chain backdoor, secret theft |
| LLM provider / prompt injection | content in and out of the model | manipulated answers, data exposure |
| Infrastructure attacker | network or host access | data theft, ransom |

## Trust boundaries

1. Phone ↔ API (public internet).
2. API ↔ internal services (cluster network).
3. Staff admin ↔ API (VPN only).
4. Backend ↔ third parties: LLM provider (abroad), SMS providers, FCM/APNs, store review.
5. CI ↔ repository ↔ registry ↔ cluster (supply chain).
6. People ↔ knowledge base (authors and reviewers).

## STRIDE by boundary

### 1. Phone ↔ API

| Threat | Mitigation | Ref |
|---|---|---|
| S: account takeover via SMS code | Kratos code flow, short code TTL, attempt limits, device list with "sign out everywhere" | ADR-0008 |
| S: SMS pumping / mass sign-up | number-range allowlist, PoW captcha, per-number/IP/device limits, daily budget breaker, conversion monitoring | ADR-0008, gaps |
| T: tampered requests, IDOR | authorization in the service layer on every object, UUIDs, deny by default, contract validation | AGENTS.md |
| R: member denies an action (SOS, listing) | server-side event log with pseudonymous actor | ADR-0021 |
| I: data on a shared or inspected phone | neutral push/SMS, optional PIN, quick exit, optional decoy mode, no sensitive data in notifications or app switcher | decisions T8 |
| I: location leak from photos | EXIF stripped on client and server; only item photos allowed | ADR-0015, ADR-0019 |
| I: contact leak between members | contacts never shown; direct chat after mutual consent; blocks mean mutual invisibility | ADR-0016, ADR-0019 |
| D: crisis/SOS unreachable | offline queue with retries, crisis endpoints exempt from forced update, separate rate limits | ADR-0018 |
| E: old client exploits | support window, forced update for vulnerabilities, signed OTA | ADR-0018 |

### 2. API ↔ internal services

| Threat | Mitigation | Ref |
|---|---|---|
| I: database or backup leak | per-user envelope encryption, blind indexes, crypto-shredding on deletion | ADR-0009 |
| T: lost or duplicated events | transactional outbox, idempotent consumers, durable timers | ADR-0007, ADR-0020 |
| I: lateral movement | encrypted in-cluster traffic, network policies, least-privilege DB roles, no DDL for the app role | ADR-0006, ADR-0011 |
| D: data loss by migration or failure | destructive-operation gate, expand/contract, PITR, restore drills | ADR-0006 |

### 3. Staff admin ↔ API

| Threat | Mitigation | Ref |
|---|---|---|
| S: stolen staff credentials | VPN, OIDC with WebAuthn, MFA verified via `acr`/`amr`, short sessions, back-channel logout | ADR-0008 |
| I: snooping by staff | sensitive fields hidden by default; reveal = audit entry; psychologists see AI chats only for their taken escalation | requirements § 6 |
| R: staff denies an action | append-only audit log | ADR-0006 |
| E: mentor exceeding rights | room-scoped, limited moderation rights, no personal data | ADR-0016 |

### 4. Backend ↔ third parties

| Threat | Mitigation | Ref |
|---|---|---|
| I: personal data to the LLM | consent gate, anonymizer with tests, only anonymized text stored | ADR-0013, ADR-0017 |
| T: prompt injection (user text, KB content) | KB only from reviewed versions; model output is data; fixed crisis response; JSON output validated; injection cases in evals | ADR-0017, ADR-0022 |
| T: harmful or wrong advice | rules in the system prompt, grounding only on retrieved sources, judge + psychologist sign-off | ADR-0017 |
| D: LLM outage | L1 lexicon and crisis response independent of the LLM | crisis-path skill |
| I: content in push/SMS | content-free notifications; one-time links | decisions T7 |

### 5. Supply chain

| Threat | Mitigation | Ref |
|---|---|---|
| T: malicious dependency | FOSS-only, pinned versions, govulncheck/OSV scan, review of new deps | ADR-0012 |
| T: malicious agent instructions | vendored, reviewed, pinned skills; tool pre-approvals removed; external agents sandboxed without keys | THIRD_PARTY_NOTICES.md |
| T: compromised CI | actions pinned to SHAs, read-only default token, secrets only in deploy jobs, signed images with SBOM | architecture § CI |
| I: secrets in the repository | gitleaks in CI, `.env` ignored | AGENTS.md |

### 6. Knowledge base

| Threat | Mitigation | Ref |
|---|---|---|
| T: unreviewed or stale content | review by role, expiry (legal 6 months), expired legal content excluded from AI | ADR-0022 |
| T: malicious author | publication requires a reviewer; versions and audit | ADR-0022 |

## Abuse cases specific to this product

- **Finding a mother who left an abusive home:** district only, never address or geolocation; blocks are invisible; no photo avatars; neutral notifications.
- **Predator posing as a mother:** verification by video call, trust levels, children never offered to unverified or non-acquainted members (N5), item-only photos, legal hold for illegal content.
- **Scams and "miracle cures":** link and phone filters in chats and listings, moderation before publication, reports.
- **Fundraising fraud in SOS:** no money collection in chats or listings; SOS feed without payment details.

## ASVS mapping

Level 2 for the whole system; level 3 for authentication, session management, cryptography, data protection and the crisis path. The ASVS checklist is part of wave 4 acceptance; each ADR lists the controls it implements.
