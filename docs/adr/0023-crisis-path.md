# ADR-0023: Crisis path and escalations

Status: Accepted · Date: 2026-10-27

## Context
A mother in crisis must reach a person quickly, whatever fails around her: the AI provider, push delivery, a staff member who does not answer. Requirements: § 5 (pipeline, fixed crisis response), § 6 (escalation queue), 15-minute backup.

## Decision
- **Context `safety`** owns crisis detection (level-1 lexicon), escalations and the duty roster. Other contexts (assistant, wellbeing, emergency, chat) call its exported service; none implements its own crisis logic.
- **Lexicon** (`safety/crisis`): normalisation (case, Kazakh and Russian letter variants, repeated letters, common typos) then phrase and pattern matching per language. The production lexicon is data loaded from a path in configuration and kept outside the public repository (gaps: open lexicon); the repository has a small demo lexicon and the evaluation sets (ADR-0017).
- **Escalation lifecycle:** `open → taken → closed`. Opening is idempotent per member while one is open. The open escalation, its `safety.EscalationOpened` event, the alert and a 15-minute timer are created in one transaction. Taking cancels the timer; the timer firing publishes `safety.EscalationUnattended` and alerts the backup shift.
- **Alerts to staff** go through `notify` as critical deliveries to `staff_role` recipients. Safety resolves a role to the current shifts of the duty roster; each shift is an external recipient (its on-duty phone, stored encrypted), so the alert is an SMS sent immediately, independent of the admin app being open. The admin queue is the second channel.
- **No personal data in the queue.** The list shows source, time and district only. Name and phone are revealed only to the staff member who took the escalation, by an explicit call that writes an audit entry (`revealEscalationContact`, `Cache-Control: no-store`).
- **The client never waits for the server** to show the crisis screen and phone numbers; `requestHumanHelp` only adds a person.

## Consequences
The duty roster must always have a primary and a backup shift; a monitoring alert fires when the next 24 hours are not covered. Staff authentication (OIDC) is required before the staff operations can be used.
