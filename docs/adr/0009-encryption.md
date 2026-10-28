# ADR-0009: Envelope encryption with per-user keys

Status: Accepted · Date: 2026-10-27

## Context
Sensitive fields (names, phones, child cards, check-in answers, notes) must be encrypted at the application level; account deletion must also make the data unreadable in backups.

## Decision
- Tink AEAD; one data key (DEK) per user, wrapped by a master key (KEK) held in OpenBao Transit. Deleting the DEK makes the user's data unreadable in the live database at once and on other replicas after the key cache expires. Backups also contain the wrapped DEK, so copies in backups stay readable until those backups expire (14 days); the consent text must say so. Wrapped DEKs are bound to their member through associated data.
- Blind indexes (HMAC with a separate key) for lookups on encrypted fields (phone).
- TLS 1.3 with hybrid post-quantum key exchange (X25519MLKEM768); encrypted traffic inside the cluster.
- OpenBao unseal keys are split (Shamir, e.g. 3 of 5) between people, with an offline copy of the master key. Key recovery is drilled like backups.
- No hand-rolled cryptography.

## Addendum: KEK rotation (2026-10-28)
- A deployment knows one primary KEK and any number of previous ones (`BIRGE_KEK_PREVIOUS_KEYSET_FILES`). New data keys are wrapped with the primary; a key wrapped by a previous KEK opens and is re-wrapped with the primary when it is first loaded (a conditional update that loses to a concurrent re-wrap or shred). The data keys never change, so no ciphertext is rewritten.
- Rotation is two rollouts: first every replica learns the new KEK as a previous one, then it becomes primary (during that second rollout a replica not yet updated re-wraps what it loads with its own primary; harmless, since both keyrings open both); a retired KEK leaves the keyring only when no live row names it (`docs/development.md`, KEK rotation). Members inactive since the rotation keep the old wrapping until a re-wrap job exists (roadmap).
- An unreadable previous keyset stops the process at start.

## Consequences
Losing the master key loses all data; key custody is an operational duty, not an afterthought.
