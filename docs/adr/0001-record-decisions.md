# ADR-0001: Record architecture decisions

Status: Accepted · Date: 2026-10-27

## Context
Work is done by a small team and by AI agents in parallel. Without a written record, settled questions get reopened and agents re-propose rejected options.

## Decision
Every decision that is costly to reverse gets an ADR in `docs/adr/`, numbered, never rewritten. A changed decision gets a new ADR that supersedes the old one.

## Consequences
Agents read the relevant ADRs before proposing changes to a decided area. A PR that contradicts an ADR must add a superseding ADR.
