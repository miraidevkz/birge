# Chat real-time protocol, v1

WebSocket at `GET /v1/chat/ws` (ADR-0016). The member session token is sent in the first frame, never in the URL.

All frames are JSON objects with a `type`. Unknown types are ignored by both sides.

## Client → server

| type | fields | meaning |
|---|---|---|
| `auth` | `token` | first frame; the server closes with 4401 if invalid |
| `subscribe` | `rooms: [{roomId, afterSeq}]` | start receiving; the server first sends every visible message with `seq > afterSeq` |
| `send` | `roomId`, `clientId` (UUIDv7), `text` | same rules as `POST /v1/chat/rooms/{roomId}/messages`; repeats of `clientId` are ignored |
| `ping` | — | keep-alive |

## Server → client

| type | fields | meaning |
|---|---|---|
| `message` | the fields of `ChatMessage` (see OpenAPI) next to `type` | a new or changed message (hidden, deleted, restored) |
| `ack` | `clientId`, `messageId`, `seq`, `status` | result of a `send` (`visible` or `held`) |
| `crisis` | `crisisPhones`, `escalationId` | sent only to the author when the crisis lexicon fires; nothing is shown to others |
| `error` | `code` | `rate_limited`, `slow_mode`, `muted`, `banned`, `not_member`, `invalid` |
| `pong` | — | keep-alive |

## Rules

- Ordering is by `seq` per room; delivery is at-least-once; the client deduplicates by `id`.
- After a reconnect the client subscribes with the last `seq` it has; the server fills the gap from PostgreSQL.
- Messages of members blocked in either direction are never sent.
- No read receipts, no typing indicators.
- Push notifications for rooms are content-free and respect room mute.
