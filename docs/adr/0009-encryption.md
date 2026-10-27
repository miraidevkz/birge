# ADR-0009: Envelope encryption with per-user keys

Status: Accepted · Date: 2026-10-27

## Context
Sensitive fields (names, phones, child cards, check-in answers, notes) must be encrypted at the application level; account deletion must also make the data unreadable in backups.

## Decision
- Tink AEAD; one data key (DEK) per user, wrapped by a master key (KEK) held in OpenBao Transit. Deleting the DEK crypto-shreds the user's data everywhere, including backups.
- Blind indexes (HMAC with a separate key) for lookups on encrypted fields (phone).
- TLS 1.3 with hybrid post-quantum key exchange (X25519MLKEM768); encrypted traffic inside the cluster.
- OpenBao unseal keys are split (Shamir, e.g. 3 of 5) between people, with an offline copy of the master key. Key recovery is drilled like backups.
- No hand-rolled cryptography.

## Consequences
Losing the master key loses all data; key custody is an operational duty, not an afterthought.
