# Staff handbook (`apps/admin`)

Plain-language guide for psychologists, moderators, lawyers and admins using the staff console. The admin is a separate web app from the mom app. In production it is reachable only through the deployment VPN. Technical flows: [scenarios.md](../scenarios.md). System overview: [overview.md](../overview.md).

Phone examples below use the synthetic shape `+70000000000`. Never put real numbers, names or message text into tickets, chat tools or screenshots outside the audited product.

## Roles at a glance

| Role | Escalations | SOS queue | Duty roster | Moderation queue | Members and rooms | Knowledge | Audit log and metrics |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Psychologist | List, take, reveal, close | No | View; alerts use psychologist / backup phones | No | No | Self-help: draft, review, publish | No |
| Moderator | Same as psychologist | List, seen, close | View | Approve / reject (and verification); legal hold | View members; give / take the mentor role; assign / remove room mentors | No | No |
| Lawyer | No | No | No | No | No | Rights: draft, review, publish | No |
| Admin | Same queue roles as above where granted | List, seen, close | Add / cancel shifts | Same as moderator | Same as moderator; lift suspensions | Topics of either kind; review-due for both | Read the log; staff list and cards; pilot metrics and CSV |

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

## SOS requests

Who: **moderator** or **admin**, with MFA. Other roles do not see the **SOS** tab; the server answers them 403.

A member's SOS of a practical kind (no one to leave the child with, an item or medicine, other) lands here. "I feel very bad" is not here: it is an escalation (see above). **You are not the only one who got it.** At the moment she raised it, the SOS went at once to the moderators (critical alert, SMS to the moderator on duty) and to every **confirmed** trusted contact of hers (invited, not yet confirmed contacts get nothing). If she shared it, verified mothers of her district also see it in the district feed and can answer "I can help". A **childcare** SOS ("No one to leave the child with") is never shared to the district: only her trusted contacts and the moderators see it, and the card says so.

1. Open **SOS**. **Open** shows the oldest first; **Closed** the newest first; **Show more** loads the next page. The open list refreshes every 30 seconds while it is on screen and when you come back to the tab. There is no sound: keep the tab open on duty, the SMS alert is the wake-up.
2. Read the card: kind, her text, first name, district, when it was raised and when it closes by itself (24 hours by default), how many offers of help she has, whether it was shared to the district, and whether a moderator has already marked it seen.
3. Press **Seen** when you take it into your attention. Only the first "Seen" is kept and shown ("Seen first by …"), so other moderators know someone is on it. Pressing it again changes nothing.
4. Decide what she needs. Offers of help come from neighbours and trusted contacts; she chooses whom to open a chat with. If the text shows a crisis (she or the child is in danger, she says she cannot cope), treat it as one: follow the escalation path and the emergency numbers, do not wait for the auto-close.
5. **Close** when the need is met, she has found help, or the request is no longer current (for example a duplicate). The screen asks once more. Closing takes it out of the open queue and out of her district feed, and cancels the automatic close. She does **not** get a notification, so do not close an SOS that is still a live need just to clear the queue. If you do nothing, it closes by itself at the auto-close time.

Every SOS shown to you, every "Seen" and every "Close" is written to the audit log with your staff ID, so the list is not something to open "just to look".

Audited: `emergency_sos.view_staff` (each SOS shown, and repeats), `emergency_sos.seen`, `emergency_sos.close_staff`.

### Never do this with SOS

- Do not suggest leaving a child with anyone except her trusted contacts or a member she has confirmed she met in person (AGENTS.md non-negotiable 5). Do not offer a neighbour from the feed for a childcare SOS.
- Do not copy her text or name into messengers, email or screenshots outside the product.
- Do not close an SOS that still needs help to make the queue look tidy.

## Moderation queue

Who: **moderator** or **admin**, with MFA.

1. Open **Moderation**. Items are oldest first: listings, specialists, reviews, chat reports, held chat messages, verification requests. The chips at the top show one kind at a time (or all); **Show more** loads the next page.
2. Read the **summary** (content under review) and the photo, if the item has one. It must not contain contact data; still treat it as sensitive and do not paste it elsewhere. Held chat messages show why they were held (new member, a link, a phone number, possible crisis); reports show the first report reason and how many reports are open.
3. **Approve** or **Reject** with a reason code (`rules_medicine`, `rules_money`, `person_in_photo`, `unsafe`, `spam`, `inaccurate`, `duplicate`, `other`). A decision is final: if someone decided first, the screen says so and refreshes the list.
4. **Verification** requests: **Approve and verify** makes her a verified member. Reject only with `inaccurate`, `duplicate` or `other`.
5. For chat **reports** and **held chat messages**: approve = keep the message (report dismissed, held message published); reject = take the message down. The reject sheet has two extra switches:
   - **Also suspend the author** (`blockAuthor`): she can no longer post or write in chats until an admin lifts the suspension. Use it for serious or repeated cases. The screen asks once more before sending.
   - **Keep for legal review** (`legalHold`, ADR-0019): the content is preserved for the lawyer even after it is taken down.

### Legal hold on a single chat message

On report and held-message items the card also has **Put on legal hold** and **Release legal hold** (both confirmed). They act on that one message, without deciding the item. The API does not show whether a message is already on hold, so choose the action you need; both are safe to repeat. Use legal hold when the lawyer asks to preserve a message (for example for a police request), and release it when the lawyer says the case is closed.

Audited: `chat.message.legal_hold_on`, `chat.message.legal_hold_off`.

Audited (decisions): `moderation.approve` / `moderation.reject`, and when used `moderation.block_author`, `moderation.legal_hold`.

### Never do this in moderation

- Do not punish or expose people outside the product tools (no public call-outs with real names).
- Do not approve content that shows a person in a photo when policy forbids it, or that pressures someone to leave a child with an unverified person.
- Do not store queue text in personal files.

## Members

Who: **moderator** or **admin**, with MFA. Every time the list is read, the server writes it to the audit log, so open it when you need it, not "just to look".

1. Open **Members**. Choose **Verified** or **Suspended**; optionally pick a district. **Show more** loads the next page.
2. Each row shows her first name, district and when she was verified or suspended. Nothing else (no phone, children, needs or check-ins) is available here, by design.
3. **Lift suspension** (suspended list, **admin** only): confirm, and she can post and write in chats again. A moderator who tries gets the message that only an admin can do it.

4. **Mentor role** (verified members who are not suspended): **Make mentor** gives her the mentor role, so she can then be assigned to a room in **Rooms**. **Remove mentor role** takes it away, and she stops being a mentor in every room she mentors. Both ask for a confirmation. The list does not show whether she already has the role, so choose the action you need; both are safe to repeat. If the screen says the role can be given only to a verified member who is not suspended, check her status first.

If the screen says "This list is not available on this server yet" (or that the mentor role cannot be changed on this server yet), the server has not been updated with this feature; nothing is wrong with your account.

Audited: mentor role given / removed (the code appears in the audit log once the server serves it).

## Rooms and mentors

Who: **moderator** or **admin**, with MFA.

1. Open **Rooms**. It lists the district and topic group rooms (never private conversations) with the number of members and the current mentors.
2. **Add mentor** opens the list of verified members. Tap one to make her a mentor of that room. Only a member who already holds the mentor role can be assigned; otherwise the screen explains it and nothing changes. The picker says so and has **Open Members**, where the role is given (see Members, step 4). **Back to rooms** leaves without a change.
3. **Remove** next to a mentor's name, then confirm, takes the mentor role in that room away from her.

Audited: `chat.room.mentor_assign`, `chat.room.mentor_remove`. As with Members, "not available on this server yet" means the server has not been updated yet.

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

## Audit log and staff cards

Who: **admin** only, with MFA. Other roles do not see the **Audit** tab; if someone opens it anyway, the server refuses and the screen says that only an admin can see it. Reading the log is itself audited (`audit.read`).

1. Open **Audit**. Staff actions are listed newest first: time, who (name, or the first characters of their ID when the IdP sends no name), their role, the action, the object with the first characters of its ID, and the reason when there is one. **Show more** loads older entries.
2. Filter by **Person**, **Action** and **Object**, or by a date range in UTC (`YYYY-MM-DD`; both days are included). Entries never contain message text, names of members or phones, only codes and IDs.
3. An action or object the console does not know yet shows as "Other action" / "Other object" with its code underneath; that is normal after a server update that added a new action.
4. Tap the person's name to open their **staff card**: roles, when they were first and last seen, and how many times they did each action in the last 30 days. **Show their actions** goes back to the log filtered to that person. **Back** returns to where you were.
5. Where the console has a screen for the object, the entry has **Open in …** (Escalations, Moderation, Members, Rooms, Duty, Knowledge). Members cannot be filtered by one person there, so you get the list.
6. **Staff** (button at the top of the log) lists everyone who has signed in to this deployment, most recently active first. The IdP still decides who is staff; this list is only for reading the log. A person appears after their first action in the console; their name, roles and "last seen" are refreshed at most once an hour, so a role change at the IdP shows here up to an hour later (access itself follows the IdP at once). The name is what the IdP sends as the display name or username; an email address is never shown or stored.

Entries by members (for example a member contact opening a shared plan) and by the system (account deletion) are not in this log. One exception is on its way: when an external trusted contact (someone without the app) opens a sent emergency plan by its one-time link, the server records "Opened a sent emergency plan by its link" with the role "External trusted contact". Such rows appear here once the server's audit list includes that role; a role the console does not know yet shows as "Other role" with its code underneath. Neither the log nor the staff list is ever deleted: they are kept as the record of who did what.

If the log or a card says "not available on this server yet", the server has not been updated with this feature.

## Pilot metrics

Who: **admin** only, with MFA. Other roles do not see the **Metrics** tab; if someone opens it anyway, the screen says that only an admin can see metrics. Opening the metrics and downloading the CSV are both audited ("Viewed pilot metrics", "Downloaded pilot metrics"), so open them when you need a figure, not to watch them change.

1. Open **Metrics** and choose a period: **Last 4 weeks**, **Last 3 months**, **This year so far**, or **Custom dates** (`YYYY-MM-DD`, both days included). A period can be at most 12 months long; a longer one is not sent and the screen says how to shorten it. The line under the picker shows the period being read.
2. The sections follow the pilot's metrics: **Activity** (members active at least once per week and per month, as bars), **Retention** (of members whose first active day falls in the period, the share still active 4 and 8 weeks later), **Tea meetings** (requests, matches both accepted, pairs who met), **SOS** (raised, share closed, median minutes to the first response), **Escalations** (opened, share taken within 15 minutes), **Rights** (answered from the knowledge base, share rated helpful), **Stress** (PSS-4 at the first check-in and about 8 weeks later: members, mean, median) and **Isolation** (before and after). Each section says in one line what it counts. Shares are percentages; times are minutes.
3. **"Hidden" means privacy, not zero.** Any number built from fewer than *k* members (the line above the sections says which *k* this server uses, usually 5) is replaced by "Fewer than k members — hidden to protect privacy". In a small pilot a count of two or three could point to a real person, so the server never sends it; the screen never shows such a cell as 0 or as a blank. A hidden week or month in the activity bars has no bar. Rights and isolation may be fully hidden on today's server; that is expected, and the CSV marks the same cells as suppressed.
4. **Download CSV** saves the same metrics for the chosen period as a file (`birge-metrics-<from>-<to>.csv`, one row per metric and week or month; hidden rows have empty numbers and `suppressed=true`). Treat the file like any other report from the product: keep it on work devices, and never combine it with lists of members to work out who is behind a number.

If the screen says "not available on this server yet", the server has not been updated with metrics.

## Profile

Shows your subject and roles. Use **Sign out** when you leave a shared machine.

## What is always audited (summary)

| Action | Audit meaning |
| --- | --- |
| Take / reveal / close escalation | Who took responsibility, who saw contact, outcome |
| SOS shown / seen / closed by staff | Who saw which SOS, who marked it seen first, who closed it |
| Duty add / cancel | Who changed on-duty phones and windows |
| Moderation decision | Approve/reject codes; optional block author / legal hold |
| Legal hold on a message | Who put or released the hold |
| Members list read, suspension lifted | Who looked at the list; who lifted whose suspension |
| Mentor assigned / removed | Who changed a room's mentors |
| Mentor role given / removed | Who changed whether a member can be a mentor |
| Audit log read | Which admin read the log and when |
| Pilot metrics viewed / downloaded | Which admin read or exported the metrics, and when |
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
