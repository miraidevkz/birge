// Package service implements the chat use cases (ADR-0016,
// docs/requirements/mvp.md § 4.4 "Chats"): district and topic rooms that
// verified members join themselves, direct rooms opened by a tea match, an
// accepted SOS response or an accepted listing contact, messages with
// idempotent sends and per-room sequences, safety holds, blocks, reports,
// room moderation, the content-free push and retention.
//
// Other contexts and the wiring may rely on:
//
//   - EnsureDistrictRoom / EnsureTopicRoom: idempotent group-room creation
//     from reference data;
//   - HandleRoomEvent / OpenDirectRoom: one direct room per origin and pair,
//     each event handled once (platform.processed_events);
//   - HandleCrisisEscalation, HandlePush, HandleRetention, EnsureRetentionTimer:
//     the timer handlers (adapter.TimerHandlers) and the first retention run;
//   - CloseReports / SetLegalHold: for the moderation context's decisions.
//
// # Sequence numbers
//
// Each room row carries last_seq. A send increments it with UPDATE ...
// RETURNING in the transaction that inserts the message, which takes the
// room row lock until commit: concurrent senders in one room queue on that
// lock, and a transaction that rolls back gives its number back, so the
// committed sequence is gap-free and strictly increasing. A separate
// sequence table would need the same lock; a PostgreSQL SEQUENCE is not
// transactional and leaves gaps. The cost is that sends to one room are
// serialised for the length of the insert transaction, which is short (no
// network call happens inside it). Lock order: the author's advisory lock
// (rate limit, idempotent replay), then the room row; moderation takes only
// the room row.
//
// # Safety
//
// Every message passes the safety crisis lexicon (port Crisis) before
// anything else and before it can become visible. A hit stores the message
// held (never shown to others, never published to moderation), returns the
// fixed crisis notice with the configured crisis phones to the author only,
// and opens a safety escalation with source "chat": synchronously after
// commit, with a chat.CrisisEscalation timer from the insert transaction as
// the retry. Crisis messages bypass rate limit, slow mode and mutes; the
// crisis path never depends on the LLM or on membership. Otherwise links
// and phone numbers, and a newcomer's first NewcomerHeld group-room
// messages, are held and published as chat.MessageHeld for moderation.
//
// # Privacy and blocks
//
// The text is sealed with the author's data key (associated data
// chat.messages.text|<message id>), so deleting her account makes her
// messages read as "deleted". Events, fan-out notices and logs carry IDs
// and codes only. Messages of members blocked in either direction are never
// returned or pushed (ADR-0019); block lookups fail closed.
package service
