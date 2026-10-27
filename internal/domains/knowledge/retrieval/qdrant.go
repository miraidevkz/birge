package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultQdrantTimeout = 30 * time.Second
	maxQdrantBody        = 32 << 20
)

// PointPayload is stored with every indexed chunk (ADR-0010).
type PointPayload struct {
	ArticleID uuid.UUID `json:"article_id"`
	VersionID uuid.UUID `json:"version_id"`
	Lang      string    `json:"lang"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	SourceURL string    `json:"source_url,omitempty"`
	Text      string    `json:"text"`
}

// Point is one vector ready to upsert.
type Point struct {
	ID      uuid.UUID
	Vector  []float32
	Payload PointPayload
}

// SearchHit is one search result with score and payload.
type SearchHit struct {
	Score   float32
	Payload PointPayload
}

// QdrantIndex talks to the Qdrant REST API. Collection is the physical
// collection name (includes the embedding model id). Alias, when set, is
// what Search uses so a model change can switch traffic without downtime
// (see package doc).
type QdrantIndex struct {
	BaseURL    string
	Collection string
	Alias      string
	HTTPClient *http.Client
}

// CollectionName builds the physical collection name from alias and model id.
// Slashes in Hugging Face model ids become underscores.
func CollectionName(alias, modelID string) string {
	alias = strings.TrimSpace(alias)
	modelID = strings.TrimSpace(modelID)
	if alias == "" {
		alias = "birge-kb"
	}
	safe := strings.ReplaceAll(modelID, "/", "_")
	safe = strings.ReplaceAll(safe, " ", "_")
	if safe == "" {
		return alias
	}
	return alias + "--" + safe
}

// EnsureCollection creates the collection when it does not exist. size is
// the embedding dimension (taken from the first vector on first upsert when
// callers already know it). Idempotent.
func (q *QdrantIndex) EnsureCollection(ctx context.Context, vectorSize int) error {
	if vectorSize <= 0 {
		return fmt.Errorf("qdrant: vector size must be positive")
	}
	path := "/collections/" + url.PathEscape(q.Collection)
	code, _, err := q.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if code == http.StatusOK {
		return nil
	}
	if code != http.StatusNotFound {
		return fmt.Errorf("qdrant: get collection: status %d", code)
	}
	body := map[string]any{
		"vectors": map[string]any{"size": vectorSize, "distance": "Cosine"},
	}
	code, _, err = q.do(ctx, http.MethodPut, path, body)
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("qdrant: create collection: status %d", code)
	}
	return q.ensureAlias(ctx)
}

// ensureAlias points Alias at Collection when Alias is set (create_alias is
// idempotent enough for our use: a conflict is ignored after a GET check).
func (q *QdrantIndex) ensureAlias(ctx context.Context) error {
	if q.Alias == "" || q.Alias == q.Collection {
		return nil
	}
	body := map[string]any{
		"actions": []any{
			map[string]any{
				"create_alias": map[string]any{
					"collection_name": q.Collection,
					"alias_name":      q.Alias,
				},
			},
		},
	}
	code, _, err := q.do(ctx, http.MethodPost, "/collections/aliases", body)
	if err != nil {
		return err
	}
	// 200 ok; 409 / already exists is fine for repeated starts.
	if code != http.StatusOK && code != http.StatusConflict {
		return fmt.Errorf("qdrant: create alias: status %d", code)
	}
	return nil
}

// Upsert writes or replaces points. Creates the collection from the first
// vector's dimension when needed.
func (q *QdrantIndex) Upsert(ctx context.Context, points []Point) error {
	if len(points) == 0 {
		return nil
	}
	if err := q.EnsureCollection(ctx, len(points[0].Vector)); err != nil {
		return err
	}
	raw := make([]map[string]any, 0, len(points))
	for _, p := range points {
		raw = append(raw, map[string]any{
			"id":      p.ID.String(),
			"vector":  p.Vector,
			"payload": p.Payload,
		})
	}
	code, _, err := q.do(ctx, http.MethodPut, "/collections/"+url.PathEscape(q.Collection)+"/points?wait=true", map[string]any{
		"points": raw,
	})
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("qdrant: upsert: status %d", code)
	}
	return nil
}

// DeleteByVersion removes every point whose payload.version_id matches.
func (q *QdrantIndex) DeleteByVersion(ctx context.Context, versionID uuid.UUID) error {
	if versionID == uuid.Nil {
		return fmt.Errorf("qdrant: version id required")
	}
	body := map[string]any{
		"filter": map[string]any{
			"must": []any{
				map[string]any{
					"key":   "version_id",
					"match": map[string]any{"value": versionID.String()},
				},
			},
		},
	}
	code, _, err := q.do(ctx, http.MethodPost, "/collections/"+url.PathEscape(q.Collection)+"/points/delete?wait=true", body)
	if err != nil {
		return err
	}
	// Missing collection: nothing to delete.
	if code == http.StatusNotFound {
		return nil
	}
	if code != http.StatusOK {
		return fmt.Errorf("qdrant: delete: status %d", code)
	}
	return nil
}

// Search finds the top limit points matching lang and kinds, with score at
// least threshold. Uses Alias when set, otherwise Collection.
func (q *QdrantIndex) Search(ctx context.Context, vector []float32, lang string, kinds []string, limit int, threshold float32) ([]SearchHit, error) {
	if len(vector) == 0 || limit <= 0 {
		return nil, nil
	}
	name := q.Collection
	if q.Alias != "" {
		name = q.Alias
	}
	must := []any{
		map[string]any{"key": "lang", "match": map[string]any{"value": lang}},
	}
	if len(kinds) == 1 {
		must = append(must, map[string]any{"key": "kind", "match": map[string]any{"value": kinds[0]}})
	} else if len(kinds) > 1 {
		should := make([]any, 0, len(kinds))
		for _, k := range kinds {
			should = append(should, map[string]any{"key": "kind", "match": map[string]any{"value": k}})
		}
		must = append(must, map[string]any{"should": should})
	}
	body := map[string]any{
		"vector":          vector,
		"limit":           limit,
		"score_threshold": threshold,
		"with_payload":    true,
		"filter":          map[string]any{"must": must},
	}
	code, raw, err := q.do(ctx, http.MethodPost, "/collections/"+url.PathEscape(name)+"/points/search", body)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound {
		return nil, nil
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("qdrant: search: status %d", code)
	}
	var resp struct {
		Result []struct {
			Score   float32      `json:"score"`
			Payload PointPayload `json:"payload"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("qdrant: decode search: %w", err)
	}
	out := make([]SearchHit, 0, len(resp.Result))
	for _, r := range resp.Result {
		out = append(out, SearchHit{Score: r.Score, Payload: r.Payload})
	}
	return out, nil
}

func (q *QdrantIndex) do(ctx context.Context, method, path string, body any) (int, []byte, error) {
	base, err := url.Parse(strings.TrimRight(q.BaseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return 0, nil, fmt.Errorf("qdrant: invalid base URL")
	}
	// path may include a query (e.g. points?wait=true).
	ref, err := url.Parse(path)
	if err != nil {
		return 0, nil, fmt.Errorf("qdrant: path: %w", err)
	}
	endpoint := base.ResolveReference(ref).String()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("qdrant: marshal: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("qdrant: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	client := q.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultQdrantTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("qdrant: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxQdrantBody))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("qdrant: read: %w", err)
	}
	return resp.StatusCode, raw, nil
}
