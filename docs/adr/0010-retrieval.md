# ADR-0010: Retrieval: versioned embeddings

Status: Accepted · Date: 2026-10-27

## Context
The knowledge base starts small (tens of documents) but the embedding model and content will change as stakeholders review and extend it.

## Decision
Embeddings are computed by a self-hosted embedding server (TEI; Infinity as fallback) and stored in Qdrant with `model_id` on every vector. A model change builds a new collection and switches an alias. Only documents with `reviewed_by` are indexed.

## Consequences
No downtime and no manual re-indexing on a live system. To verify: current TEI license.
