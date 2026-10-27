package retrieval

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
)

// DefaultScoreThreshold drops weak matches (cosine on normalised e5 vectors).
const DefaultScoreThreshold = float32(0.3)

// Hit is one retrieved passage for the assistant (wiring maps it to the
// assistant Fragment type; this package must not import assistant).
type Hit struct {
	ArticleID uuid.UUID
	Title     string
	SourceURL string
	Text      string
}

// Retriever embeds the query and searches the index.
type Retriever struct {
	Embedder       Embedder
	Index          Index
	ScoreThreshold float32
}

// Search returns up to k passages in lang. Scope "rights" limits to kind
// rights; any other scope searches both rights and selfhelp.
func (r *Retriever) Search(ctx context.Context, lang, scope, text string, k int) ([]Hit, error) {
	if k <= 0 || text == "" {
		return nil, nil
	}
	if !model.LangIsValid(lang) {
		return nil, fmt.Errorf("retrieval: invalid lang %q", lang)
	}
	kinds := []string{model.KindRights, model.KindSelfhelp}
	if scope == model.KindRights || scope == "rights" {
		kinds = []string{model.KindRights}
	}
	vec, err := r.Embedder.EmbedQuery(ctx, text)
	if err != nil {
		return nil, err
	}
	threshold := r.ScoreThreshold
	if threshold == 0 {
		threshold = DefaultScoreThreshold
	}
	hits, err := r.Index.Search(ctx, vec, lang, kinds, k, threshold)
	if err != nil {
		return nil, err
	}
	out := make([]Hit, 0, len(hits))
	seen := map[uuid.UUID]bool{}
	for _, h := range hits {
		if h.Payload.ArticleID == uuid.Nil || seen[h.Payload.ArticleID] {
			// One fragment per article keeps the assistant sources list tidy.
			continue
		}
		seen[h.Payload.ArticleID] = true
		out = append(out, Hit{
			ArticleID: h.Payload.ArticleID,
			Title:     h.Payload.Title,
			SourceURL: h.Payload.SourceURL,
			Text:      h.Payload.Text,
		})
		if len(out) >= k {
			break
		}
	}
	return out, nil
}
