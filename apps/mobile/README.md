# apps/mobile

The Birge app for mothers: iOS, Android and web (PWA) from one Expo Router code base ([ADR-0004](../../docs/adr/0004-react-native-clients.md)). No Expo cloud services: builds run locally or in our CI with `expo prebuild` and Gradle/Xcode; telemetry is off (`EXPO_NO_TELEMETRY=1` in every script).

## Run

From the repository root, once:

```sh
npm ci                    # installs every workspace; install scripts are disabled (.npmrc)
cp apps/mobile/.env.example apps/mobile/.env   # then edit the URLs
```

Then:

```sh
npm run start -w @birge/mobile   # Metro; open in a development build or the browser
npm run web -w @birge/mobile     # browser only
npm run web-build                # static web export to apps/mobile/dist, with a PWA check
npm run check                    # what CI runs for clients (also `make app-check`)
```

Native builds (`expo prebuild`, then Gradle or Xcode) are not part of `npm run check`; the release maintainer runs them.

## Configure

All configuration comes from the environment at build time ([`app.config.ts`](app.config.ts)); the values are public and embedded in the bundle, so nothing secret belongs there.

| Variable                     | Meaning                                                                                     | Default                      |
| ---------------------------- | ------------------------------------------------------------------------------------------- | ---------------------------- |
| `EXPO_PUBLIC_API_URL`        | API root with version, e.g. `https://api.example.invalid/v1`                                | `http://localhost:8080/v1`   |
| `EXPO_PUBLIC_KRATOS_URL`     | the auth gateway in front of Kratos, e.g. `https://api.example.invalid/auth`                | `http://localhost:8080/auth` |
| `EXPO_PUBLIC_CRISIS_PHONES`  | JSON `[{"number","label"}]` shown before the app has ever reached the server                | `[]`                         |
| `BIRGE_APP_ID`               | store identifier for native builds                                                          | `invalid.example.birge`      |
| `BIRGE_GOOGLE_SERVICES_FILE` | path to the deployment's `google-services.json` (Android FCM push); never in the repository | unset: no push on Android    |

`src/lib/config.ts` validates them at startup.

## Structure

```
src/app/            routes only (Expo Router): _layout, (tabs)/{index,circle,help,profile},
                    chat/{index,[roomId]}, listings, specialists, privacy, knowledge, assistant,
                    sign-in, sos, checkin, checkin-history, emergency-plan, inbox,
                    trusted-invitation/[contactId], trusted-sos/[sosId], shared-plan/[ownerId],
                    account-deleted, plan (web, external contact)
src/screens/        screen bodies: sign-in, home, help (crisis), circle, chat-rooms, listings,
                    specialists, profile, privacy, account-deleted, knowledge, assistant, sos,
                    checkin, checkin-history, emergency-plan, trusted-invitation, trusted-sos, shared-plan,
                    shared-plan-card, inbox, notification-row, external-plan, placeholder,
                    signed-in gate
src/lib/            app services: config, session, api, storage (.web), crisis-phones, help-outbox,
                    query, routes, plan-link, format, wellbeing-text, inbox, notification-text,
                    notification-targets, push (.web)
public/             web shell: index.html (links the manifest), manifest.webmanifest, icon
scripts/            check-web-build.mjs
```

- **Session.** The Kratos session token lives in `expo-secure-store` (iOS Keychain, Android Keystore); on the web in `localStorage` (see `storage.web.ts` for why the web build must have a strict CSP). Tabs wait for the stored session before redirecting to sign-in.
- **SOS entry.** Every `Screen` shows it; it opens `/sos` (raise/close SOS, crisis phones), which is outside the tabs and works signed out. "I feel very bad" is `requestHumanHelp` with source `sos`, not `POST /me/sos`.
- **Crisis path.** Help and SOS always show “If there is a threat to life — call 112” with a `tel:112` button from the `help` locale, independent of the crisis phone list and of the network; last-known numbers still appear below. Signed out, request actions explain that sending to a moderator needs sign-in and link to `/sign-in`; 112 and the phones never need it. "Contact a person" goes through the outbox ([ADR-0018](../../docs/adr/0018-client-compatibility.md), [ADR-0023](../../docs/adr/0023-crisis-path.md)). No animation on help or SOS. Check-in `flag=crisis` reuses `HelpScreen` with source `checkin`.
- **Sign-in.** Uses `Screen layout="form"` so the title, explanation, fields and primary button are one vertically centred, keyboard-aware group.
- **Check-in / emergency plan.** Home opens `/checkin`; history is `/checkin-history`. Profile opens `/emergency-plan` (child card, trusted contacts, send).
- **Trusted contact side.** `/trusted-invitation/<contactId>` (inbox `emergency.trusted_invitation`) reads `getTrustedInvitation` and names who invited her by first name. An open invitation offers "Accept" (`acceptTrustedInvitation`), "Not now" (only leaves; the invitation stays open) and "Decline"; a confirmed one says she is already a contact and offers "Stop being her trusted contact". Both decline and stop call `declineTrustedInvitation` after a `ConfirmSheet` that says she will no longer get the SOS alerts or the plan and that the inviter is not notified. 404 (on the read, accept or decline) says the invitation is no longer active, 403 on accept that the meeting in person must be confirmed first, other failures keep the screen with a retry. `/trusted-sos/<sosId>` (inbox `emergency.sos_raised_contact`, param `sos_id`) shows `getTrustedSos`: her first name, the type, the text, when it was sent, the status and the auto-close time, every type including childcare, stated plainly (a confirmed trusted contact is exactly who may help with the child; the screen never suggests anyone else and never shows or asks for a phone number). "I can help" calls `respondAsTrustedContact`; then, or when `respondedByMe` is set, it says "You offered help" and that the author may open a chat (she accepts the response as any other, `acceptSosResponse`). A closed SOS (status, or 409 on respond) gets a calm text and no action; 403 and 404 say only "not available now"; a network or server error offers a retry. It is a crisis screen (`Screen variant="crisis"`, no transition animation) with the same "call 112" banner as `/sos`; every read is audited, so the query has `gcTime: 0`, `staleTime: Infinity` and no refetch on focus or reconnect, and the answer is kept locally instead of reading the SOS again. `/shared-plan/<ownerId>` (inbox `emergency.plan_sent`) shows `getSharedPlan` with `SharedPlanCard`, the same card as the external page; it is crisis-adjacent (`Screen variant="crisis"`, no transition animation), keeps nothing in the query cache (`gcTime: 0`, every read is audited) and answers 403 and 404 alike ("not available now"). All three routes check the id as a UUID before any request.
- **Account deletion.** After `requestDeletion` succeeds, Privacy signs out (a failing sign-out step does not matter: the request is accepted) and replaces the stack with `/account-deleted`: what is deleted and when (within 24 hours, keys shredded), one button back to the start. A root of the stack (`ROOT_ROUTES`), no `SignedIn` gate, no network.
- **External plan page.** `/plan#<token>` is the link in the plan SMS for an external trusted contact (no account). Web only (native redirects home), outside the signed-in flow: no `SignedIn` gate, no onboarding redirect, no tabs, a root of the stack (`ROOT_ROUTES`, no Back) and `Screen variant="crisis"` (no SOS entry, no animation). On mount `takePlanToken` reads the fragment, removes it with `history.replaceState` and checks the 43-character base64url shape; the page posts it to `viewExternalPlan` through `publicApi`, the client that never sends the member session (a shared browser may hold one), and shows it with `SharedPlanCard`. The token stays in memory only for a retry after a network error: never stored, logged or used as a query key. 404 (also a missing or malformed token) says the link has expired, 429 asks for a pause, a network or server error offers a retry. The tab title is the same neutral title.
- **Inbox.** `/inbox` lists `listInbox` newest first with "Show more" cursor paging; Home shows the newest five and a button to all of them with the unread count, and the Home tab carries the unread badge (the tab bar is full, so the inbox has no tab of its own). Title and body come from the `inbox` locale namespace by template (`templates.<template>.title|body`); an unknown template gets the generic `fallback`, never the raw code. Opening an item calls `markRead` (the cache updates at once, then the inbox is refetched) and goes to the item's place from the one table in `lib/notification-targets.ts`; params are used only when they are UUIDs, and a template without a screen opens nothing. Every inbox query lives under the `['inbox']` key with `staleTime: 0`, so screen focus, the app returning to the foreground and a push received in the foreground refresh all of them.
- **Push** (iOS and Android; `push.web.ts` does nothing on the web). `expo-notifications` (MIT, the Expo SDK module; pinned to the version SDK 58 bundles) is used only for the permission, the **native** device token (`getDevicePushTokenAsync`: FCM on Android, APNs on iOS; no Expo push service) and tap handling. The permission is asked after the first-run setup (once per phone) or from the Profile toggle, never at first launch. The device is registered with `registerDevice` under a random device ID made per sign-in, again on every start while push is on (tokens rotate), and removed with `unregisterDevice` before sign-out and account deletion (`SessionProvider beforeSignOut`, bounded to 5 s); the ID is forgotten when a session expires. Android without `BIRGE_GOOGLE_SERVICES_FILE` never asks; a refused permission, a missing token or a failed registration skip quietly and the inbox still works. A push carries only `notification_id` (FCM `data`, APNs top level) and a neutral title; a tap fetches the newest items and opens that item's place, or the inbox. A push in the foreground shows its neutral title and refreshes the inbox; the app never schedules a local notification. iOS builds need the Push Notifications capability (the config plugin adds the `aps-environment` entitlement).
- **Data.** React Query for reads; retries only what can succeed; a 401 ends the session.
- **UI.** Only `@birge/ui` components and tokens in screens; texts only from `@birge/i18n`.
