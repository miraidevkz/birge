# @birge/api

The typed client for the Birge HTTP API, the offline outbox and member sign-in. Free of native modules: the app passes in storage, randomness and hashing.

## API client

`src/schema.gen.ts` is generated from [`contracts/openapi/birge.yaml`](../../contracts/openapi/birge.yaml) by openapi-typescript. It is committed and never edited by hand ([ADR-0005](../../docs/adr/0005-contracts-first.md)).

```sh
npm run api-gen     # regenerate after a contract change
npm run api-check   # fails when the committed file is stale (also part of npm test)
```

`createApiClient` wraps openapi-fetch and sets, on every request:

- `Authorization: Bearer <session token>` when signed in;
- `X-App-Version` and `X-App-Platform` ([ADR-0018](../../docs/adr/0018-client-compatibility.md));
- a fresh UUIDv7 `Idempotency-Key` on POST, PUT, PATCH and DELETE, unless the caller passes one (a replay).

These headers are removed from the generated parameter types, so callers neither must nor can pass them by accident. Everything else is the contract's types:

```ts
const inbox = await unwrap(api.GET('/me/inbox', { params: { query: { limit: 5 } } }));
```

`unwrap` returns the data or throws `ApiError` (HTTP error, RFC 9457 problem mapped to `status` and `code`) or `NetworkError` (no answer). `isRetryable(err)` says whether repeating can help: network errors, 401 (after a new sign-in), 408, 425, 429 and 5xx.

## Outbox

`createOutbox` stores requests that must arrive even if the phone is offline at the moment of the press (today `requestHumanHelp`). An entry is persisted before the first attempt, keeps its idempotency key, and leaves only when the server answered with success or a non-retryable error. Stored entries are replayed by later app versions, so operation names and the stored format are part of the contract: never rename or reinterpret one, add a new operation.

## Sign-in

`createKratosClient` runs the Ory Kratos native login and registration flows with the SMS `code` method through the auth gateway (`internal/domains/membership/authgw`). Every submission carries a fresh ALTCHA proof-of-work solution (`solvePow`). Unknown phone numbers switch from login to registration automatically. Errors are `SignInError` with a `code` the screen maps to a locale text; neither the number nor the code is logged or put into an error.
