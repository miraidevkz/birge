# <context>

One paragraph: what this bounded context is for and which requirement it serves (`docs/requirements/mvp.md` § …).

## Owns

| Table (schema `<context>`) | Purpose | Personal data |
|---|---|---|
| | | none / encrypted (which columns) |

## API

Operations of tag `<context>` in `contracts/openapi/birge.yaml`, with the rules that are not visible in the contract (authorization, limits, idempotency).

## Events

| Direction | Event | When | Guarantees |
|---|---|---|---|
| publishes | `<context>.<Event>` | | outbox, same transaction |
| consumes | | | deduplicated by event id |

## Timers

| Kind | Scheduled when | Handler does | Idempotency |
|---|---|---|---|

## Service API for other contexts

Exported functions in `service/` and what callers may rely on.

## Configuration

| Variable | Default | Meaning |
|---|---|---|

## Invariants

Rules that must always hold (and where they are enforced: database constraint, service, test).

## Failure modes

What happens when the database, the broker or an external provider is down, and how the context recovers.

## Tests

Where the unit and integration tests are and what they cover.
