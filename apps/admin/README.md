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
src/app/            routes only (Expo Router): _layout, (tabs)/{index,moderation,duty,knowledge/*,profile}, sign-in
src/screens/        screen bodies: sign-in, escalations, moderation, duty, knowledge-*, profile
src/lib/            app services: config, auth (token in memory), session, api, query, platform-crypto, knowledge-problem
public/             web shell: index.html, manifest.webmanifest, icon
scripts/            check-web-build.mjs
```

## Screens

| Screen               | Route                         | Role                                                                                   |
| -------------------- | ----------------------------- | -------------------------------------------------------------------------------------- |
| Escalations          | `(tabs)/`                     | Open / taken / closed queue; reveal contact; close with outcome                        |
| Moderation           | `(tabs)/moderation`           | Approve / reject queue items                                                           |
| Duty                 | `(tabs)/duty`                 | Current-week roster; add / delete shifts                                               |
| Knowledge articles   | `(tabs)/knowledge`            | List versions (kind + status filters); create topic; open editor / review / review-due |
| Knowledge editor     | `(tabs)/knowledge/editor`     | Draft create / update / submit; new version from review-due                            |
| Knowledge review     | `(tabs)/knowledge/review`     | `in_review` queue; publish or reject with reason                                       |
| Knowledge review due | `(tabs)/knowledge/review-due` | Published versions past review-due; open editor for a new version                      |
| Profile              | `(tabs)/profile`              | Subject, roles, sign out                                                               |
| Sign-in              | `/sign-in`                    | OIDC + MFA message                                                                     |

- **Session.** The OIDC access token lives in process memory only (never `localStorage` or SecureStore). A reload requires sign-in again. On API 401 the token is cleared; on 403 `mfa_required` the sign-in screen shows the second-factor message.
- **Auth.** Authorization code + PKCE via `expo-auth-session` against `EXPO_PUBLIC_STAFF_ISSUER` discovery, client `EXPO_PUBLIC_STAFF_CLIENT_ID`, scopes `openid offline_access`.
- **Data.** React Query for reads; the open escalations list polls every 15 s while focused. Contact details from `revealEscalationContact` stay in component state only, never in the query cache.
- **UI.** Only `@birge/ui` components and tokens in screens; texts only from the `admin` namespace of `@birge/i18n`. Escalations use `Screen variant="crisis"` (no animation). No SOS entry (staff console is not the member crisis path). Knowledge 403/409 map to `knowledge.problem.*` locale keys; the client never assumes publish or review permission.
