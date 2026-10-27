package wiring

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"

	knowledgeadapter "birge/internal/domains/knowledge/adapter"
	knowledgeexport "birge/internal/domains/knowledge/export"
	"birge/internal/domains/knowledge/retrieval"
	"birge/internal/platform/clock"
	platformdb "birge/internal/platform/db"
)

// NewRetrieval builds the embedder, Qdrant index, retriever and indexer when
// cfg.RetrievalEnabled; otherwise returns a NoRetrieval searcher and a nil
// indexer. Callers log RetrievalDisabled themselves at start.
func NewRetrieval(cfg Config, pool *pgxpool.Pool, js jetstream.JetStream, clk clock.Clock, logger *slog.Logger) (searcher retrievalSearcher, indexer *retrieval.Indexer) {
	if !cfg.RetrievalEnabled {
		return retrieval.NoRetrieval{}, nil
	}
	emb := &retrieval.TEIEmbedder{
		BaseURL:       cfg.TEIURL,
		QueryPrefix:   cfg.TEIQueryPrefix,
		PassagePrefix: cfg.TEIPassagePrefix,
	}
	idx := &retrieval.QdrantIndex{
		BaseURL:    cfg.QdrantURL,
		Collection: cfg.QdrantCollection(),
		Alias:      cfg.QdrantAlias,
	}
	arts := &retrieval.StoreArticles{
		Store: &knowledgeadapter.PostgresStore{Pool: pool},
		Clock: clk,
	}
	ret := &retrieval.Retriever{
		Embedder: emb, Index: idx, ScoreThreshold: cfg.QdrantScoreThreshold,
	}
	var ix *retrieval.Indexer
	if js != nil {
		ix = &retrieval.Indexer{
			JS: js, Stream: StreamName, Articles: arts,
			Embedder: emb, Index: idx, Logger: logger,
		}
	}
	return ret, ix
}

// retrievalSearcher is the common Search surface of Retriever and NoRetrieval.
type retrievalSearcher interface {
	Search(ctx context.Context, lang, scope, text string, k int) ([]retrieval.Hit, error)
}

// RunKBReindex rebuilds the Qdrant collection from every current published
// fragment. Requires RetrievalEnabled configuration.
func RunKBReindex(ctx context.Context, logger *slog.Logger, cfg Config) error {
	if !cfg.RetrievalEnabled {
		return fmt.Errorf("kb-reindex: TEI, Qdrant and BIRGE_EMBEDDING_MODEL_ID are required")
	}
	pool, err := platformdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("kb-reindex: %w", err)
	}
	defer pool.Close()
	searcher, _ := NewRetrieval(cfg, pool, nil, clock.System{}, logger)
	ret, ok := searcher.(*retrieval.Retriever)
	if !ok || ret == nil {
		return fmt.Errorf("kb-reindex: retrieval not configured")
	}
	arts := &retrieval.StoreArticles{
		Store: &knowledgeadapter.PostgresStore{Pool: pool},
		Clock: clock.System{},
	}
	indexer := &retrieval.Indexer{
		Articles: arts, Embedder: ret.Embedder, Index: ret.Index, Logger: logger,
	}
	n, err := indexer.Reindex(ctx)
	if err != nil {
		return fmt.Errorf("kb-reindex: %w", err)
	}
	logger.Info("kb-reindex finished", "versions", n, "collection", cfg.QdrantCollection())
	return nil
}

// RunKBExport writes every current published knowledge version as markdown
// under outDir (ADR-0022). Requires DATABASE_URL only.
func RunKBExport(ctx context.Context, logger *slog.Logger, cfg Config, outDir string) error {
	pool, err := platformdb.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("kb-export: %w", err)
	}
	defer pool.Close()
	w := &knowledgeexport.Writer{
		Store: &knowledgeadapter.PostgresStore{Pool: pool},
		Now:   clock.System{}.Now,
	}
	n, err := w.Export(ctx, outDir)
	if err != nil {
		return fmt.Errorf("kb-export: %w", err)
	}
	logger.Info("kb-export finished", "versions", n, "out", outDir)
	return nil
}
