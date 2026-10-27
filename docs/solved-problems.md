# Lessons from solved problems

Rules for writing code, each learned from a real bug. Check new code and reviews against them. When a fix teaches something new, add or sharpen a rule here (one line or two, plus the commit that taught it); skip fixes that teach nothing reusable.

## Identity and ownership

- **Contexts other than membership key data by `MemberID`, never `IdentityID`.** The identity provider's ID only finds or creates the member. Using it elsewhere shreds a key that does not exist on account deletion and hides a member's own notifications. A member principal in tests carries both IDs, like the real middleware. (247f73e privacy, notify; d2cd720 safety.)
- **A reference names its owner.** When a stored reference can point into different contexts, store its kind per owning context (`member`, `external` trusted contact, `staff` shift), so a later lookup asks the right owner. (a0515e5: a duty shift resolved as a trusted contact.)
- **A record created lazily starts with optional fields.** A field required in the contract must exist from the first response; a member created on first sign-in has no name yet. (b163756)
- **A record created lazily is deleted as a tombstone, not a delete.** Otherwise the next request of the same identity (a session still valid, or replayed after the identity was deleted) creates it again under a new ID. Keep the key column and a deletion marker, and refuse it on lookup. (account deletion: `membership.members.deleted_at`.)

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

## Contracts and code generation

- **Quote YAML strings that contain punctuation in flow style.** A comma inside `{description: ...}` split it into an extra field and invalidated the whole OpenAPI document; the generator accepted it, the validator did not. The validator's tests load the real contract. (68739b9) A bulk fix must touch only `description` keys that describe a schema, not properties named `description`. (The validator caught a second case before commit.)
- **Do not name a column after a type or keyword (`text`, `type` in some positions).** psqldef's parser rejects `text text`, so `make migration` fails at merge; use `content`. (emergency and directory reviews)
- **Scope each sqlc block to its own schema file.** A block reading the whole `db/schema` directory copies other contexts' models into its package. (190cb6e)
- **A middleware that reads the body must hand it on; test that the handler receives it unchanged.** (9796572)

## Process

- **Merge only on the check's exit code.** A pipeline that filters output with `grep` hides a failing test; read `$?` of `make go-check` before merging. (68739b9 fixed what this let through.)
- **Task branches never contain migrations.** Agents edit their schema file; the tech lead generates the migration on the rebased branch, runs `make go-check` there and only then fast-forwards `main`, so `main` never holds a schema without its migration. (190cb6e review; 36e9631 fixed a merge that went first.)
