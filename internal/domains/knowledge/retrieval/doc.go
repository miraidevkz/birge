// Package retrieval indexes published knowledge into Qdrant (ADR-0010) and
// answers the assistant's retrieval needs. Embeddings come from a self-hosted
// TEI server; vectors live in a Qdrant collection named for the embedding
// model. Only the standard library is used.
//
// What it owns:
//
//   - TEIEmbedder: POST {base}/embed with {"inputs":[...]}, optional
//     query/passage prefixes for e5-style models.
//   - QdrantIndex: create collection if missing, upsert points with payload
//     {article_id, version_id, lang, kind, title, source_url}, delete by
//     version_id, search with lang+kind filter and a score threshold.
//   - Indexer: consumes knowledge.ArticlePublished (chunk ≈800 characters
//     with overlap, embed, upsert) and knowledge.ArticleExpired (delete);
//     idempotent by deterministic point ids. Reindex rebuilds from a
//     published-fragment source (birge kb-reindex).
//   - Retriever: embed the query, search top-k; scope "rights" → kind
//     rights only, otherwise both kinds. Returns Hit values; wiring adapts
//     them to the assistant Retriever port (archtest: this package must not
//     import assistant).
//   - NoRetrieval: empty results when TEI or Qdrant are not configured;
//     wiring logs Warn event=RetrievalDisabled at start.
//
// Collection naming and model changes (ADR-0010):
//
//	collection = {alias}--{model_id with '/' → '_'}
//	example:    birge-kb--intfloat_multilingual-e5-base
//
// To switch embedding models without downtime: create and fill the new
// collection, then point the alias at it:
//
//	PUT /collections/aliases
//	{"actions":[{"create_alias":{"collection_name":"<new>","alias_name":"<alias>"}}]}
//
// Search always uses the alias; the previous collection can be dropped after
// the switch. Re-run birge kb-reindex after changing BIRGE_EMBEDDING_MODEL_ID.
//
// Invariants: only published, non-expired versions are indexed; repeated
// publish of the same version upserts the same point ids; expire deletes by
// version_id. Logs carry IDs and event names, never article bodies.
package retrieval
