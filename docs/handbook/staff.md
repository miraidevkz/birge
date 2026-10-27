# Staff handbook (`apps/admin`)

Plain-language guide for psychologists, moderators, lawyers and admins using the staff console. The admin is a separate web app from the mom app. In production it is reachable only through the deployment VPN. Technical flows: [scenarios.md](../scenarios.md). System overview: [overview.md](../overview.md).

Phone examples below use the synthetic shape `+70000000000`. Never put real numbers, names or message text into tickets, chat tools or screenshots outside the audited product.

## Roles at a glance

| Role | Escalations | Duty roster | Moderation queue | Knowledge |
| --- | --- | --- | --- | --- |
| Psychologist | List, take, reveal, close | View; alerts use psychologist / backup phones | No | Self-help: draft, review, publish |
| Moderator | Same as psychologist | View | Approve / reject (and verification) | No |
| Lawyer | No | No | No | Rights: draft, review, publish |
| Admin | Same queue roles as above where granted | Add / cancel shifts | Same as moderator | Topics of either kind; review-due for both |

The API enforces roles and MFA. If you see “MFA required”, complete the second factor and sign in again. Deny by default: wrong role → 403.

## Sign in

1. Open the staff admin on the VPN (production) or your local URL (development).
2. Choose **Sign in**. The browser runs OIDC authorization code + PKCE against the configured issuer (Ory Hydra or another mapped IdP).
3. Complete MFA when asked (WebAuthn / configured `acr`/`amr`). Without MFA the API returns 403 `mfa_required`.
4. Your access token stays in browser memory only. Reloading the page means signing in again. Sign out from **Profile**.

What is audited: staff API actions that change state (take, reveal, close, moderation decisions, duty changes, knowledge publish/reject, suspensions). Sign-in itself is handled by the IdP.

## Duty shifts

Who: **admin** adds and cancels; psychologists and moderators can **view** the roster.

1. Open the **Duty** tab (current ISO week).
2. To add a shift: staff UUID, role (`psychologist` or `moderator`), start/end, primary or backup, and an E.164 on-duty phone (example: `+70000000000`).
3. The phone is stored encrypted and is **never shown again** in the roster API. It is used only so notify can SMS the person on duty when an escalation or SOS needs them.
4. Cancel a shift that has not started (admin). Started shifts cannot be deleted.

Coverage: primary and backup psychologist coverage should always exist. ADR-0023 asks for a monitoring alert when the next 24 hours are uncovered; that alert is **not implemented yet** (safety README). Until it is, check the roster manually.

Audited: `duty_shift.add`, `duty_shift.cancel`.

## Handling an escalation

Escalations come from help requests and crisis paths (assistant, check-in, SOS button for “contact a person”, menu, chat) — not from every practical SOS. The open list shows **source, time, status, district only**. No names or phones until you take and explicitly reveal.

1. Open **Escalations** (home tab). Prefer the **open** filter; the list refreshes about every 15 seconds while focused.
2. **Take** the case you will handle. That makes you responsible and cancels the 15-minute backup timer. Only you can reveal contact or close it. If someone else already took it → conflict.
3. **Reveal contact** only when you are ready to call. The reveal is audited (`escalation.reveal_contact`) before name and phone are returned. Details stay on screen state only — not in shared caches.
4. Call using the product UI. Do not paste the number into other apps, personal notes or group chats.
5. **Close** with an outcome: `contacted`, `referred`, `emergency_services`, `no_answer`, or `not_a_crisis`. Optional short note (encrypted; visible only to staff with access to that escalation).

If nobody takes an open escalation within about 15 minutes, the backup psychologist shift is SMS-alerted (`safety.EscalationUnattended`).

Audited: `escalation.take`, `escalation.reveal_contact`, `escalation.close`.

### Never do this on escalations

- Do not copy name, phone or notes into email, messengers, paper left on a desk, or screenshots for “convenience”.
- Do not ask the member to leave a child with a neighbour from the app, a stranger, or anyone who is not already a **trusted contact** or a member she has confirmed she **met in person** (AGENTS.md non-negotiable 5).
- Do not delay taking a case hoping someone else will; the backup SMS is a safety net, not a workflow.
- Do not use reveal “just to look” without intending to contact.

## Moderation queue

Who: **moderator** or **admin**, with MFA.

1. Open **Moderation**. Items are oldest first: listings, specialists, reviews, chat reports, held chat messages, verification requests.
2. Read the **summary** (content under review). It must not contain contact data; still treat it as sensitive and do not paste it elsewhere.
3. **Approve** or **Reject** with a reason code (`rules_medicine`, `rules_money`, `person_in_photo`, `unsafe`, `spam`, `inaccurate`, `duplicate`, `other`). A decision is final (retry → already decided).
4. For chat **reports**: approve = keep the message (report dismissed); reject = take the message down. The API also supports `blockAuthor` (suspend the author) and `legalHold` (preserve content for legal review, ADR-0019). The current admin screen focuses on approve/reject; legal hold can also be set via staff chat endpoints when your process requires it.

Audited: `moderation.approve` / `moderation.reject`, and when used `moderation.block_author`, `moderation.legal_hold`.

### Never do this in moderation

- Do not punish or expose people outside the product tools (no public call-outs with real names).
- Do not approve content that shows a person in a photo when policy forbids it, or that pressures someone to leave a child with an unverified person.
- Do not store queue text in personal files.

## Knowledge: draft, review, publish, review-due

Who: **psychologist** (self-help), **lawyer** (rights), **admin** (topics of either kind; sees both review-due lists).

1. **Articles** — create a draft for the right kind and language (`kk` / `ru` / `en`). Edit only while status is draft (author only).
2. **Submit** — draft → in review.
3. **Review** tab — another person with the matching role (not the author) **publishes** or **rejects** with a reason. Rights articles need a `sourceUrl` before publish. Publish retires the previous published version of that article+language and marks other languages as needing sync.
4. **Review due** — published versions whose review-due timer has fired (about one month before expiry). Open the editor to start a new version. Rights expire after about 6 months, self-help after about 12. Expired rights are shown to members as “being re-checked” and are kept out of the AI index.
5. Published text can be exported to the public `knowledge/` tree for other deployments and evaluations (`birge kb-export`). Drafts never leave the admin that way.

Staff push/SMS when review-due fires is not wired yet; use the Review due screen.

Audited: publish and reject (reviewer and time recorded on the version).

### Never do this in knowledge

- Do not publish your own draft.
- Do not publish legal answers without a source, or after they are out of date.
- Do not put personal data about real members into articles.
- Do not invent clinical or legal advice the reviewer has not signed off.

## Profile

Shows your subject and roles. Use **Sign out** when you leave a shared machine.

## What is always audited (summary)

| Action | Audit meaning |
| --- | --- |
| Take / reveal / close escalation | Who took responsibility, who saw contact, outcome |
| Duty add / cancel | Who changed on-duty phones and windows |
| Moderation decision | Approve/reject codes; optional block author / legal hold |
| Knowledge publish / reject | Who reviewed what version |
| Shared emergency plan view (member side) | Separate member audit when a trusted contact opens a sent plan |
| Account deletion | System actors per context after `privacy.DataDeletionRequested` |

Logs and events carry IDs and event names, not message bodies, phones or sealed fields.

## If something looks wrong

- Escalation without SMS: check duty coverage and that notify/SMS plugins are configured ([roadmap.md](../roadmap.md) still lists provider plugins as open).
- Cannot reveal contact: only the taker, and only while status is `taken`; after account deletion the shell may remain but contact is gone.
- 401 on every call: session expired — sign in again.
- 403 `mfa_required`: complete MFA.
- 403 on knowledge publish: wrong role or you are the author.

When in doubt, prefer not copying personal data and ask the deployment’s admin or on-call engineer — do not bypass the product into private channels.
