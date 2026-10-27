# ADR-0019: Member blocking, media policy and illegal-content incidents

Status: Accepted · Date: 2026-10-27

## Context
Members can be harassed by other members, including people they know offline. Photos of children with special needs are health data and a high-risk category for abuse.

## Decision
- **Blocking:** a member can block another member. The block is mutual invisibility across contexts: no direct messages, no tea matches, the blocked member does not see her SOS in the district feed or her messages in group rooms, and vice versa. The blocked member is not told. Blocks are stored in `membership` and every context filters by them; this is a shared policy, not a per-feature option.
- **Photos:** only photos of items are allowed (listings); photos of people, especially children, are rejected by moderation and stated in the listing rules. There are no photo avatars: initials or an illustration from a fixed set. Media pipeline per ADR-0015 (metadata stripped, optional in-jurisdiction scanner, otherwise moderation before publication).
- **Suspected illegal content involving children:** the content is hidden immediately and the author is blocked; the evidence is preserved in an isolated store with restricted, audited access and outside normal retention/deletion; the lawyer decides on reporting to the authorities. The procedure is part of the moderator runbook.

## Consequences
Deletion and retention jobs must skip items under legal hold. Every new feature that shows members to each other must apply the block filter (checked in review).
