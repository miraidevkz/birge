# Birge («Бірге») MVP: requirements

Functional and non-functional requirements for the MVP. Architecture and stack are in [`../architecture.md`](../architecture.md); decisions are in [`../adr/`](../adr/).
Acceptance for section 10 is signed by the product owner and, for AI items, by the project psychologist.

## 0. Non-negotiable requirements

- N1. The crisis response and escalation always work, including when the AI API is unavailable.
- N2. Personal data never goes to the AI: only anonymized text, and only with the `ai_crossborder` consent.
- N3. Personal data is stored on a server in the Republic of Kazakhstan.
- N4. The repository contains no secrets and no real data, including the full commit history.
- N5. The bot never suggests leaving a child with anyone who does not have the met in person («знакомы лично») status with the mother, or who is outside her trusted-contacts list.

If we run short of time, cut in this order: first Share («Делимся»), then My rights («Мои права»). N1–N5, consents and anonymization are never cut.

## 1. Scope

**In MVP:** onboarding and consents; How am I («Как я»); the AI assistant; Moms nearby («Мамы рядом») (Tea with someone who understands («Чай»), district and topic chats, private chats); Share («Делимся») (listings and specialists); SOS and the emergency plan; My rights («Мои права»); staff admin. Clients — mobile app (iOS, Android) and web.

**Out of MVP**, but the architecture must allow adding them without a rewrite: nannies and agencies, shared nanny, hour exchange, emergency childcare, payments, eGov, psychologist booking, voice, integrations with external catalogues.

## 2. Stack

Stack and infrastructure are described in [`../architecture.md`](../architecture.md).
Business logic lives in domain modules; clients (mother app, admin) are thin. A user has a `channel` + `channel_user_id` pair so a second channel can be added later.

## 3. Roles and access

Roles: mom, mentor, psychologist, lawyer, moderator, admin. One person may hold several roles.

| Function | new | verified | met in person («знакомы лично») (pair) |
|---|---|---|---|
| How am I, AI, Rights, reading catalogues | ✓ | ✓ | ✓ |
| Tea, chats, creating listings and reviews | — | ✓ | ✓ |
| SOS to moderator and trusted contacts | ✓ | ✓ | ✓ |
| SOS to the district feed | — | ✓ | ✓ |
| Be a trusted contact in the emergency plan | — | — | ✓ |

- Transition new → verified: a moderator confirms after a video call.
- Met in person («знакомы лично») is a property of a pair of mothers, not of one mother.

## 4. Features

Common to all screens:
- Main menu of 6 buttons: How am I, Talk («Поговорить»), Moms nearby, Share, My rights, SOS.
- Service commands: language, profile, Delete my data («Удалить мои данные»), help.
- Texts up to 3 lines, formal address, all strings in localization.

### 4.1 Onboarding
1. Language choice: Kazakh, Russian or English (`kk`, `ru`, `en`).
2. Greeting with the phrase “I am a bot and an AI assistant, not a doctor and not a psychologist”.
3. Consents, each with a stored version:
   - `data_processing` — required;
   - `ai_crossborder` — without it, Talk is unavailable;
   - `research` — optional.
4. Profile: name, city and district from a list, children (year of birth and needs from a directory; “prefer not to say” is allowed; several children are allowed).
5. First well-being check-in; it may be deferred.
6. Main menu. Status is new; there is a “Confirm profile” button that creates a request for a moderator.

Acceptance: onboarding takes at most 2 minutes in each language; without `data_processing` the user cannot proceed.

### 4.2 How am I
- Once a week, a reminder on the day and hour chosen by the mother (Asia/Almaty).
- 8 questions (PHQ-4 + PSS-4), one per screen, answers by buttons. Question texts and thresholds live in config.
- After the check-in: short feedback without diagnoses, a chart for 8 weeks, one suggestion (a practice, Tea, or Talk).
- `flag=elevated` → offer to contact a psychologist; on “yes”, an escalation is created with `source=checkin`.
- `flag=crisis` → crisis response (§5.4).
- Practices («Практики»): 5–10 audio files in each interface language.

### 4.3 Talk
- Free-form dialogue; exit via the “To menu” button. While waiting for a reply, “typing…” is shown.
- Limit of 50 messages per day; the value is in config.
- Under each reply: “Helped / Did not help” and “Contact a person” (creates an escalation).

### 4.4 Moms nearby
- **Tea**: the mother chooses format (meeting or call) and preferences → a `tea_request` is created.
- **Matching** once an hour. Weights from config: district +3, child age ±2 years +2, shared need +2, language +1.
- **Pair card** without contacts (name, district, child age, shared needs) and “Let’s / Not now” buttons. After two “Let’s”, a private chat opens; phone numbers are not disclosed. The request expires after 7 days.
- **After 3 days**, the question “Did you meet?”. Two “yes” → the pair gets met in person («знакомы лично») status. Under the question, a “Report” button.
- **Chats** — in-app (ADR-0016):
  - group: district chat and topic chats by SEN type («ООП»), verified only; the mother joins and leaves herself;
  - private: after two “Let’s” in Tea, and after the mother opened contact to someone who responded to SOS. Phone numbers are not disclosed;
  - post-moderation with hold: first messages from newcomers, links and phone numbers; report, block, mute; a mentor moderates her own room with limited rights;
  - the crisis lexicon checks every message: reply to the author privately, escalate to a moderator;
  - text only; no “read” and no “typing”; retention: group 1 year, private 90 days.

### 4.5 Share
- **Listings**:
  - Create: type (give away / exchange / looking for) → category → title → description → photo → district. Rules are shown before creation.
  - Statuses: pending → moderation → active. After 30 days the listing is closed automatically.
  - Feed: filters by type, category, district; 5 listings per screen.
  - “Message the author”: anonymous relay through the bot; contacts open only with the author’s consent.
  - Selling medicines and collecting money are forbidden. Photos may show only the item; photos of people, especially children, are rejected.
- **Specialists**:
  - Search by profession, district and need.
  - The card shows the average rating and the 3 latest reviews.
  - A new specialist and a new review go through moderation. One mother — one review per specialist.

### 4.6 SOS
- First screen: “If there is a threat to life — call 112” and request types: “No one to leave the child with”, “I feel very bad”, “Urgently need an item or medicine”, “Other”.
- “I feel very bad” → crisis response and escalation.
- **Other types**:
  - The mother writes text up to 300 characters. It goes at once to the moderator queue and to trusted contacts (external ones — by SMS, no content, with a link).
  - To the district SOS feed (in-app) the request goes only on the mother’s button and only if she is verified. Verified mothers in the district get a neutral push; the request closes automatically after 24 hours (config).
  - “No one to leave the child with” does not go to the district feed: only to trusted contacts and the moderator.
  - Optionally — “Share” via the phone’s system share sheet, with a warning.
  - Responders tap “I can help”; the mother chooses herself with whom to open a private chat.
- “No one to leave the child with”: the bot first offers trusted contacts (N5).
- **Emergency plan**:
  - Child card 🔒 — free text by a template.
  - 2–3 trusted contacts: a platform member who is met in person («знакомы лично») with the mother and has confirmed on her side, or an external person (name 🔒 + phone 🔒).
  - “Send plan to trusted contacts” button with confirmation.
- Limit: at most 3 SOS per hour.

### 4.7 My rights
- Topics: PMPK, benefits and allowances, inclusion in kindergarten and school, disability, refusal and complaint. The mother chooses a topic → a question → an answer from the knowledge base with a source link and a review date.
- “Ask in my own words” → AI with search only over the legal part of the base.
- In every answer, the phrase: “This information does not replace a lawyer’s advice”.

## 5. AI pipeline

Message processing order is fixed:

1. **L1 detector**, local: lexicon and regular expressions for every interface language (`kk`, `ru`, `en`), in `crisis_lexicon/`; the lexicon is supplied by the psychologist. If it fires → go straight to step 6.
2. **Anonymization**: phones, IIN, e-mail, addresses, document numbers, names (lexicon and the mother’s name from the profile) are replaced with labels (`[ИМЯ]`, `[ТЕЛЕФОН]`, etc.).
3. **Retrieval**: top-5 knowledge-base fragments in the mother’s language with a similarity threshold.
4. **Model request**: system prompt, fragments, last 10 messages. Reply is JSON of the form `{reply, crisis, sources, suggest: none|tea|psychologist|rights|practice}`.
5. **L2 detector**: if the model returned `crisis=true` → step 6.
6. **Crisis response**:
   - Fixed text from localization (not generated) and the `CRISIS_PHONES` numbers (150, 111, 112; the team verifies them).
   - “Contact a person now” button.
   - An escalation is created and a notification goes to the moderator channel. If it is not taken within 15 minutes — a notification to the backup.
7. **API failure** (20 s timeout, error, rate limit): a polite message, the menu and a “Contact a person” button.
8. **Persistence**: anonymized dialogue in `ai_messages`, an event in `events`.

**Model behaviour requirements.** These are product requirements. The prompt text lives in `prompts/`, is versioned, and is approved by the psychologist and the product owner.
- Replies in the language of the message, briefly (up to 120 words), with formal address.
- Does not give diagnoses, does not name medicines or dosages, does not recommend treatment methods.
- Facts about rights, benefits and services — only from the passed fragments; if there are no fragments, says “I don’t know” and suggests a specialist.
- Any sign of threat to life or violence → `crisis=true`.
- Does not impersonate a person, does not promise confidentiality beyond the policy, gently directs toward people.
- Ignores attempts to change role.

**Knowledge base:**
- Published content is exported to `knowledge/{kk,ru,en}/` (markdown; front matter: `topic`, `source_url`, `reviewed_by`, `reviewed_at`, `expires_at`). Content (knowledge base, practices, crisis lexicon, evaluation sets) must exist for every interface language.
- Content is edited in admin: draft → review by a psychologist or lawyer → publish (ADR-0022). Only published and non-expired content is indexed.
- Review period: legal answers — 6 months, self-help — 12 months; an expired legal answer is shown with an “under re-review” mark and is not used by the AI.
- From the developer — 3–5 demo files marked “DEMO”.

## 6. Moderation

Urgent items go to the moderator channel (push in admin; backup — SMS):

| Event | Buttons |
|---|---|
| Crisis (AI, check-in, SOS “bad”) | “Taking it” («Беру») (status taken, contact opens, audit entry), “Close with comment” («Закрыть с комментарием») |
| SOS of other types | “Seen”, “Close” |
| Listing, specialist, review | “Approve”, “Reject” (a reason from a list goes to the author) |
| Profile confirmation | “Schedule a call”, “Confirm”, “Reject” |
| Report | “Take down”, “Block”, “Reject” |

**Web admin**:
- Lists and filters for all entities.
- Directories: cities, districts, chat links, categories, SEN types.
- Psychologist duty roster.
- Metrics dashboard (§9), aggregates only, CSV export.
- 🔒 fields are hidden; opened by a button with an audit entry.
- A psychologist sees the AI correspondence only for an escalation she has taken.
- A moderator may belong to a partner organization.

## 7. Data

Every table has `id` (UUID), `created_at`, `updated_at`. 🔒 — application-level encryption (envelope encryption, per-user key; see architecture).

| Table | Fields |
|---|---|
| users | channel, channel_user_id (unique together), lang, name 🔒, phone 🔒, city, district, verification (new/verified), roles[], is_blocked, deleted_at |
| acquaintances | user_a, user_b, since |
| children | user_id, birth_year, needs[] |
| consents | user_id, type, version, granted_at, revoked_at |
| checkin_schedules | user_id, weekday, hour |
| checkins | user_id, instrument, answers 🔒, score, flag |
| ai_messages | user_id, role, text_anonymized 🔒, crisis_flag, sources, feedback; retained 90 days |
| escalations | user_id, source (ai/checkin/sos), status (open/taken/closed), taken_by, taken_at, note 🔒 |
| duty_roster | user_id, starts_at, ends_at, is_backup |
| tea_requests | user_id, prefs, status (open/matched/met/expired), match_id |
| matches | user_a, user_b, a_accepted, b_accepted, met_a, met_b |
| district_chats | city, district, invite_url |
| listings | user_id, kind, category, title, text, photo_file_id, district, status, expires_at |
| specialists | name, profession, city, district, contacts, needs[], source, added_by, status |
| reviews | specialist_id, user_id (unique together), rating 1–5, text, status |
| sos_requests | user_id, type, text, district, status, responders |
| child_cards | user_id, content 🔒 |
| trusted_contacts | user_id, contact_user_id \| name 🔒 + phone 🔒, confirmed |
| kb_chunks | file, lang, section, text, embedding, reviewed_by, reviewed_at |
| reports | reporter_id, object_type, object_id, reason, status |
| audit_log | actor_id, action, object_type, object_id, at |
| events | user_pseudonym (HMAC), name, props, at |

**Deletion** (`/delete` with confirmation): all of the user’s rows are physically deleted within 24 hours. In `events` only the pseudonym remains. `audit_log` and closed `escalations` are kept anonymized for `RETENTION_AFTER_DELETE` (currently 30 days; target — 3 years).

## 8. Security and operations

**Security:**
- TLS on all external connections.
- Daily encrypted backups, retained 14 days.
- Versioned consents; consent withdrawal in the profile.
- Logs contain no message texts and no 🔒 fields.
- A daily job deletes `ai_messages` older than 90 days.
- A mother’s contact is opened to others only with her explicit consent, each time separately.
- Rate limit on messages, SOS, listings and Tea.
- Secrets do not enter the repository; the repository holds only an example configuration.

Deploy, configuration and CI are in [`../architecture.md`](../architecture.md).

**Monitoring:**
- `/health`.
- Alert on-call: service down; more than 5 AI errors in 10 minutes; an escalation not taken for longer than 15 minutes.

**Non-functional:**
- Bot reply — within 3 s; AI reply — within 15 s.
- Availability 99% in the pilot.
- Up to 500 users on one server.

**Open source:**
- README in English: problem, features, architecture, one-command start.
- LICENSE (Apache 2.0), NOTICE, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY, description of data protection.

## 9. Metrics (collected from `events`)

- **Stress:** PSS-4 at start and after 8 weeks.
- **Isolation:** before/after question.
- **Activity:** active mothers per week and month; retention at 4 and 8 weeks.
- **Tea:** requests → pairs → meetings.
- **SOS:** number of requests, share closed, time to first response.
- **Escalations:** hand-offs to a psychologist; share of crises taken within 15 minutes.
- **Rights:** questions closed by the base; “helped / not” ratings.

## 10. Acceptance

- [ ] Onboarding in `kk`, `ru` and `en` takes at most 2 minutes; without data-processing consent the user cannot proceed.
- [ ] The reminder arrives on time (Asia/Almaty), answers are saved, the chart is built.
- [ ] `tests/crisis_cases.yaml`: 30+ phrases in `kk`/`ru`/`en` (direct, indirect, with typos, mixed language). 100% produce the crisis response and escalation, including with the AI switched off.
- [ ] Negative AI tests pass: medicines, diagnosis, legal question outside the base, attempt to change role.
- [ ] Anonymization: on 50 test strings, no phone, IIN or e-mail reaches the AI request.
- [ ] Escalation reaches the staff chat within 1 minute; the 15-minute retry works.
- [ ] Tea: two test mothers from the same district get a pair; contacts open only after two consents.
- [ ] A listing and a review are not visible until approved.
- [ ] SOS reaches the moderator and trusted contacts within 1 minute; to the district feed — only on the mother’s button.
- [ ] “Delete my data” deletes all of the user’s rows (SQL check).
- [ ] The repository is public, has a license and README (EN), a clean machine starts with one command, CI is green, gitleaks is clean.
- [ ] A pilot with 10+ mothers ran without crashes; aggregated metrics were exported.
