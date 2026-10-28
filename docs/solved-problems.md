# Lessons from solved problems

Rules for writing code, each learned from a real bug. Check new code and reviews against them. When a fix teaches something new, add or sharpen a rule here (one line or two, plus the commit that taught it); skip fixes that teach nothing reusable.

## Identity and ownership

- **Contexts other than membership key data by `MemberID`, never `IdentityID`.** The identity provider's ID only finds or creates the member. Using it elsewhere shreds a key that does not exist on account deletion and hides a member's own notifications. A member principal in tests carries both IDs, like the real middleware. (247f73e privacy, notify; d2cd720 safety.)
- **A reference names its owner.** When a stored reference can point into different contexts, store its kind per owning context (`member`, `external` trusted contact, `staff` shift), so a later lookup asks the right owner. (a0515e5: a duty shift resolved as a trusted contact.)
- **A record created lazily starts with optional fields.** A field required in the contract must exist from the first response; a member created on first sign-in has no name yet. (b163756)
- **A record created lazily is deleted as a tombstone, not a delete.** Otherwise the next request of the same identity (a session still valid, or replayed after the identity was deleted) creates it again under a new ID. Keep the key column and a deletion marker, and refuse it on lookup. (account deletion: `membership.members.deleted_at`.)
- **Authorize before the lookup.** A service method that loads a row by ID and only then checks the caller's role answers 404 for unknown IDs and 401/403 for known ones, so anyone can probe which IDs exist. Check that the caller may use the operation at all first, then load, then check the row-specific rule (kind, author). (knowledge: create draft, update, submit, publish, reject; chat: room moderation)

## Transactions and delivery

- **Side effects after commit go through a timer or the outbox created in the same transaction.** A push sent after commit is lost if the process dies in between. (190cb6e)
- **A consumer must not depend on a third service to accept an event.** Look external data (a phone from the identity provider) up when it is needed, outside database transactions; otherwise an outage exhausts redeliveries and loses the event. (792b8d9)
- **Idempotency needs a natural key.** One pending verification request or one open escalation per member is a partial unique index; `ON CONFLICT (id)` with a fresh id makes nothing idempotent. (af01fd6)

## Safety and privacy

- **Check an image's declared size before decoding it.** `image.DecodeConfig` reads only the header; refuse width×height above a limit before `image.Decode`, or a small file that claims a huge image exhausts memory. (sharing photo processing review)
- **Never drop a critical notification silently.** A budget or guard that blocks a crisis SMS fails loudly (`failed`, Error log with an event name for alerting), and the daily budget does not apply to critical messages. (190cb6e)
- **A log line never joins a person to health data.** Log a record ID (check-in, escalation), not the member ID next to a flag, score or category. (d183550)
- **Bind ciphertexts and wrapped keys to their owner.** Associated data names the field and the row (`<context>.<table>.<column>|<id>`); a wrapped data key is bound to its member and KEK, so a copied value fails to open. (23756ea)
- **A restriction on a member never closes her crisis path.** Suspension, blocks or limits stop her reaching others; the help request, her SOS, the emergency plan and her own-data routes stay open, with an e2e test. (moderation review: suspension first answered 403 on every route.)
- **Documentation claims only what the mechanism guarantees.** Crypto-shredding does not reach backups until they expire. (93c3266)

- **Tests connect as the application role, not a superuser.** The e2e database URL uses `birge_app`, so a schema missing from `db/roles.sql` fails a test instead of production; `db.TestRolesCoverEverySchema` checks every `CREATE SCHEMA` has a grant. (all context schemas but two had no grants.)

- **A browser client on the Kratos API flow needs `Origin` stripped at the gateway.** Kratos answers 400 "the request was made as part of an AJAX request" to any API flow with an `Origin` header; the native apps send none, so only the web build failed ("Could not sign in"). The auth gateway drops it, as it drops `Cookie`. Test sign-in in a browser, not only on a device. (web demo)

## Contracts and code generation

- **Quote YAML strings that contain punctuation in flow style.** A comma inside `{description: ...}` split it into an extra field and invalidated the whole OpenAPI document; the generator accepted it, the validator did not. The validator's tests load the real contract. (68739b9) A bulk fix must touch only `description` keys that describe a schema, not properties named `description`. (The validator caught a second case before commit.)
- **Do not name a column after a type or keyword (`text`, `type` in some positions).** psqldef's parser rejects `text text`, so `make migration` fails at merge; use `content`. (emergency and directory reviews)
- **Scope each sqlc block to its own schema file.** A block reading the whole `db/schema` directory copies other contexts' models into its package. (190cb6e)
- **A middleware that reads the body must hand it on; test that the handler receives it unchanged.** (9796572)

## Clients

- **Take Expo SDK versions from `npx expo install`, not from an older `expo` patch.** The compatibility list bundled with an early patch can point at release candidates (SDK 58.0.9 listed `react-native` 0.88.0-rc.4), and mixing it with current modules fails to resolve. Install the SDK's current set with `expo install`, then pin each version exactly. (apps foundation)
- **Testing Library for React Native 14 renders asynchronously.** `render`, `fireEvent.*` and `rerender` return promises; without `await` every query fails with "render function has not been called". (apps foundation)
- **Reanimated 4 needs its Jest resolver.** Without `resolver: 'react-native-reanimated/jest/resolver'` any test that imports a component using Reanimated fails while loading the native worklets module. (apps foundation)
- **A formatted input never reads its own inserted characters as the user's.** PhoneField took any '+' in the text as typed by the member; with fast typing the native text ("87") arrived before the formatted "+7" rendered, the inserted '+' counted as hers and a leading 8 stayed +8. Track user intent (typed '+' first in an empty field) in the component, not from the text. (sign-in on a device)
- **On Android, set a controlled `selection` one frame after the value it belongs to.** Given in the same render as a longer formatted text, it is applied to the old text, so the caret lands early and the next digit goes before the last one ("+7 (7" + 0 -> "+7 (07"). Unit tests cannot see it; type key by key on a device (`adb shell input keyevent`). (PhoneField)
- **Teach `tailwind-merge` every custom class group.** It took our type-ramp sizes (`text-body`, `text-title`, `text-display`) for text colours and dropped them next to `text-foreground`, so every Text rendered at the default size and the whole app looked like one small font. `cn()` extends its `font-size` group from `tokens.fontSize`; a new custom scale (sizes, radii, shadows) needs the same. (warm redesign)
- **A served build lives outside the repository tree.** `npm run check` exports `apps/mobile/dist` for localhost, and tree syncs to the stack host delete untracked files: serving `dist`, then `dist-stack`, took the public demo down twice (`ERR_CONNECTION_REFUSED`, then 404). The stack exports to `BIRGE_WEB_DIR` (default `~/.local/share/birge-web`). (web demo)
- **Forms use KeyboardAwareScrollView (react-native-keyboard-controller), not KeyboardAvoidingView.** With edge-to-edge (Expo SDK 58) Android no longer resizes the window for the keyboard (`adjustResize` is ignored), and neither `behavior="height"` nor scrolling on `keyboardDidShow` moved the sign-in fields out from under it on a Nokia 7.2. Verify keyboards on a device, never only in Jest. (sign-in)
- **The member's language choice is local and wins.** The profile used to apply the server's `lang` (default `kk`) on load and switched the app's language by itself after the first sign-in. The choice is saved on the device, offered before sign-in, and sent to the server after sign-in, never pulled back. (lib/language)
- **Export with `--clear` when `EXPO_PUBLIC_*` change.** Metro caches transformed modules with the inlined public env values, so a second `expo export` with a new API URL still shipped the old one (`localhost` in the bundle). `up.sh web` exports with `--clear`; release builds clear the Metro cache too. (dev stack behind a tunnel)
- **Go targets never use a bare `./...`.** npm packages can ship Go sources (for example `flatted`), and `./...` under the module root then vets, tests and lints them as our code. The Makefile builds the package list once (`GO_PKGS`, `go list` without `node_modules`); new Go targets use it.

## Process

- **Merge only on the check's exit code.** A pipeline that filters output with `grep` hides a failing test; read `$?` of `make go-check` before merging. (68739b9 fixed what this let through.)
- **Task branches never contain migrations.** Agents edit their schema file; the tech lead generates the migration on the rebased branch, runs `make go-check` there and only then fast-forwards `main`, so `main` never holds a schema without its migration. (190cb6e review; 36e9631 fixed a merge that went first.)
