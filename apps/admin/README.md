# apps/admin

The Birge staff console: web only (react-native-web) from Expo Router ([ADR-0004](../../docs/adr/0004-react-native-clients.md), [ADR-0008](../../docs/adr/0008-identity.md) staff addendum). Separate bundle from `apps/mobile`; shares `packages/ui`, `packages/api` and `packages/i18n`. Reachable only through the deployment VPN in production. No Expo cloud services; telemetry is off (`EXPO_NO_TELEMETRY=1` in every script).

## Run

From the repository root, once:

```sh
npm ci                    # installs every workspace; install scripts are disabled (.npmrc)
cp apps/admin/.env.example apps/admin/.env   # then edit the URLs
```

Then:

```sh
npm run start -w @birge/admin   # Metro, browser only
npm run web -w @birge/admin
npm run web-build               # static web export for mobile and admin
npm run check                   # what CI runs for clients (also `make app-check`)
```

## Configure

All configuration comes from the environment at build time ([`app.config.ts`](app.config.ts)); the values are public and embedded in the bundle.

| Variable                      | Meaning                                                      | Default                    |
| ----------------------------- | ------------------------------------------------------------ | -------------------------- |
| `EXPO_PUBLIC_API_URL`         | API root with version, e.g. `https://api.example.invalid/v1` | `http://localhost:8080/v1` |
| `EXPO_PUBLIC_STAFF_ISSUER`    | OIDC issuer (discovery document)                             | `http://localhost:4444/`   |
| `EXPO_PUBLIC_STAFF_CLIENT_ID` | public OIDC client id for authorization-code + PKCE          | `birge-admin`              |

`src/lib/config.ts` validates them at startup.

## Dependencies (new)

`expo-auth-session` and `expo-web-browser` (MIT, Expo SDK 58, pinned exactly) are the only packages added beyond the mobile set. They implement the browser OIDC authorization-code flow with PKCE against the staff issuer; the standard library and existing packages do not cover that.

## Structure

```
src/app/            routes only (Expo Router): _layout, (tabs)/{index,moderation,members,rooms,duty,knowledge/*,audit/*,profile}, sign-in
src/screens/        screen bodies: sign-in, escalations, moderation, members, rooms, duty, knowledge-*, audit, staff-list,
                    staff-card, profile
src/lib/            app services: config, auth (token in memory), session, api, query, platform-crypto, knowledge-problem,
                    not-implemented (not-yet-served and 403 detection), members (members and districts queries),
                    staff (audit log and staff account queries), audit-codes (known audit codes → locale keys),
                    audit-links (audit object → tab), stack-back (shared Back above a tab root), room-title, time
public/             web shell: index.html, manifest.webmanifest, icon
scripts/            check-web-build.mjs
```

## Screens

| Screen               | Route                          | Role                                                                                                 |
| -------------------- | ------------------------------ | ---------------------------------------------------------------------------------------------------- |
| Escalations          | `(tabs)/`                      | Open / taken / closed queue; reveal contact; close with outcome                                      |
| Moderation           | `(tabs)/moderation`            | Queue by kind with paging; approve / reject (block author, legal hold); legal hold on a chat message |
| Members              | `(tabs)/members`               | Verified / suspended members by district; lift a suspension (admin); give / take the mentor role     |
| Rooms                | `(tabs)/rooms`                 | Group rooms with member counts and mentors; add mentor (picker over verified members) / remove       |
| Duty                 | `(tabs)/duty`                  | Current-week roster; add / delete shifts                                                             |
| Knowledge articles   | `(tabs)/knowledge`             | List versions (kind + status filters); create topic; open editor / review / review-due               |
| Knowledge editor     | `(tabs)/knowledge/editor`      | Draft create / update / submit; new version from review-due                                          |
| Knowledge review     | `(tabs)/knowledge/review`      | `in_review` queue; publish or reject with reason                                                     |
| Knowledge review due | `(tabs)/knowledge/review-due`  | Published versions past review-due; open editor for a new version                                    |
| Audit log            | `(tabs)/audit`                 | Admin only: staff actions newest first; filters person / action / object / dates; open card, object  |
| Staff                | `(tabs)/audit/staff`           | Admin only: staff accounts, most recently active first; open card                                    |
| Staff card           | `(tabs)/audit/staff/[staffId]` | Admin only: roles, first / last seen, action counts for 30 days; "Show their actions"                |
| Profile              | `(tabs)/profile`               | Subject, roles, sign out                                                                             |
| Sign-in              | `/sign-in`                     | OIDC + MFA message                                                                                   |

- **Session.** The OIDC access token lives in process memory only (never `localStorage` or SecureStore). A reload requires sign-in again. On API 401 the token is cleared; on 403 `mfa_required` the sign-in screen shows the second-factor message.
- **Auth.** Authorization code + PKCE via `expo-auth-session` against `EXPO_PUBLIC_STAFF_ISSUER` discovery, client `EXPO_PUBLIC_STAFF_CLIENT_ID`, scopes `openid offline_access`.
- **Data.** React Query for reads; the open escalations list polls every 15 s while focused. Contact details from `revealEscalationContact` stay in component state only, never in the query cache.
- **UI.** Only `@birge/ui` components and tokens in screens; texts only from the `admin` namespace of `@birge/i18n`. Escalations use `Screen variant="crisis"` (no animation). No SOS entry (staff console is not the member crisis path). Knowledge 403/409 map to `knowledge.problem.*` locale keys; the client never assumes publish or review permission.
- **Not yet served.** Some endpoints have a contract before their server code. Until it lands a stub answers 501, and a route that is not mounted at all answers 404 without the problem code `not_found` (the staff audit and account routes, today). `lib/not-implemented.ts` treats both as "not available"; a 404 with `not_found` stays a real answer (for example a staff card for an unknown ID). The list then shows a calm "not available on this server yet" empty state (no error banner), and the query client never retries it. A mutation that gets such an answer (the mentor role) shows a warning banner with the same meaning.
- **Audit log and staff.** Admin only: the Audit tab is hidden from other roles (`href: null` from the profile roles), but the API decides; a 403 shows "only an admin can see this" instead of an error. Entries carry codes and IDs only. `lib/audit-codes.ts` lists the codes the server writes today (keep it in step with `internal/`) and maps them to `admin:audit.{action,object,reason}.*` (dots become underscores); an unknown code shows a generic label with the raw code on a second line. Filters: person (from `listStaffAccounts`), action and object chips over the known codes, and a UTC day range (`from` 00:00:00, `to` 23:59:59, sent only when the date is complete and valid). Tapping the person opens the staff card (a stack under the tab with the shared Back from `lib/stack-back.tsx`); "Show their actions" returns to the log with `actorId` in the route. `lib/audit-links.ts` maps an object type to the tab that shows it (escalation, moderation item, member, room, duty shift, knowledge); others get no link. Query keys carry IDs and codes, never names; every read is audited by the server, so nothing polls.
- **Moderation.** Reason codes (`holdReason`, `reportReason`) are shown only through `moderation.holdReason.*` / `moderation.reportReason.*`; an unknown code gets the generic label. `blockAuthor` and `legalHold` are offered only for reports and held chat messages, and suspending the author is confirmed once more. The API does not expose whether a message is on legal hold, so "Put on legal hold" and "Release legal hold" are two explicit, confirmed actions on the message (`objectId`).
- **Members and mentors.** Member names stay in screen data only (query keys carry status and district ID). Lifting a suspension is admin only; on 403 the screen says so. A verified, not suspended member has "Make mentor" and "Remove mentor role" (`grantMentorRole` / `revokeMentorRole`, both confirmed and idempotent). `StaffMember` does not say whether she holds the role, so both are offered, like legal hold; 409 means she is not verified or is suspended. Revoking also refreshes Rooms (chat removes her from the rooms she mentors). Assigning a mentor in Rooms answers 409 when she does not hold the mentor role; the screen explains it, and the picker says that only mentors can be assigned with a link to Members. Room titles are locale keys resolved with the mom app's `chat:roomTitle.*` and `reference:*` texts (`lib/room-title.ts`), so staff and members see the same name.
