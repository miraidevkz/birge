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

| Variable                    | Meaning                                                                      | Default                      |
| --------------------------- | ---------------------------------------------------------------------------- | ---------------------------- |
| `EXPO_PUBLIC_API_URL`       | API root with version, e.g. `https://api.example.invalid/v1`                 | `http://localhost:8080/v1`   |
| `EXPO_PUBLIC_KRATOS_URL`    | the auth gateway in front of Kratos, e.g. `https://api.example.invalid/auth` | `http://localhost:8080/auth` |
| `EXPO_PUBLIC_CRISIS_PHONES` | JSON `[{"number","label"}]` shown before the app has ever reached the server | `[]`                         |
| `BIRGE_APP_ID`              | store identifier for native builds                                           | `invalid.example.birge`      |

`src/lib/config.ts` validates them at startup.

## Structure

```
src/app/            routes only (Expo Router): _layout, (tabs)/{index,circle,help,profile},
                    chat/{index,[roomId]}, listings, specialists, privacy, knowledge, assistant,
                    sign-in, sos, checkin, checkin-history, emergency-plan, plan (web, external contact)
src/screens/        screen bodies: sign-in, home, help (crisis), circle, chat-rooms, listings,
                    specialists, profile, privacy, knowledge, assistant, sos, checkin,
                    checkin-history, emergency-plan, external-plan, placeholder, signed-in gate
src/lib/            app services: config, session, api, storage (.web), crisis-phones, help-outbox,
                    query, routes, plan-link, format, wellbeing-text
public/             web shell: index.html (links the manifest), manifest.webmanifest, icon
scripts/            check-web-build.mjs
```

- **Session.** The Kratos session token lives in `expo-secure-store` (iOS Keychain, Android Keystore); on the web in `localStorage` (see `storage.web.ts` for why the web build must have a strict CSP). Tabs wait for the stored session before redirecting to sign-in.
- **SOS entry.** Every `Screen` shows it; it opens `/sos` (raise/close SOS, crisis phones), which is outside the tabs and works signed out. "I feel very bad" is `requestHumanHelp` with source `sos`, not `POST /me/sos`.
- **Crisis path.** Help and SOS always show “If there is a threat to life — call 112” with a `tel:112` button from the `help` locale, independent of the crisis phone list and of the network; last-known numbers still appear below. Signed out, request actions explain that sending to a moderator needs sign-in and link to `/sign-in`; 112 and the phones never need it. "Contact a person" goes through the outbox ([ADR-0018](../../docs/adr/0018-client-compatibility.md), [ADR-0023](../../docs/adr/0023-crisis-path.md)). No animation on help or SOS. Check-in `flag=crisis` reuses `HelpScreen` with source `checkin`.
- **Sign-in.** Uses `Screen layout="form"` so the title, explanation, fields and primary button are one vertically centred, keyboard-aware group.
- **Check-in / emergency plan.** Home opens `/checkin`; history is `/checkin-history`. Profile opens `/emergency-plan` (child card, trusted contacts, send).
- **External plan page.** `/plan#<token>` is the link in the plan SMS for an external trusted contact (no account). Web only (native redirects home), outside the signed-in flow: no `SignedIn` gate, no onboarding redirect, no tabs, a root of the stack (`ROOT_ROUTES`, no Back) and `Screen variant="crisis"` (no SOS entry, no animation). On mount `takePlanToken` reads the fragment, removes it with `history.replaceState` and checks the 43-character base64url shape; the page posts it to `viewExternalPlan` through `publicApi`, the client that never sends the member session (a shared browser may hold one). The token stays in memory only for a retry after a network error: never stored, logged or used as a query key. 404 (also a missing or malformed token) says the link has expired, 429 asks for a pause, a network or server error offers a retry. The tab title is the same neutral title.
- **Data.** React Query for reads; retries only what can succeed; a 401 ends the session.
- **UI.** Only `@birge/ui` components and tokens in screens; texts only from `@birge/i18n`.
