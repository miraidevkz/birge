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
)

const (
	defaultTEITimeout = 30 * time.Second
	maxTEIBody        = 16 << 20 // 16 MiB: large batches of vectors
)

// TEIEmbedder calls a self-hosted text-embeddings-inference server
// (POST {base}/embed with {"inputs":[...]}). QueryPrefix / PassagePrefix
// are prepended for e5-style models (typically "query: " / "passage: ");
// leave them empty when the model does not need prefixes.
type TEIEmbedder struct {
	BaseURL       string
	QueryPrefix   string
	PassagePrefix string
	HTTPClient    *http.Client
}

// EmbedQuery embeds one search query (with QueryPrefix when set).
func (e *TEIEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.embed(ctx, []string{e.QueryPrefix + text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("tei: expected 1 vector, got %d", len(vecs))
	}
	return vecs[0], nil
}

// EmbedPassages embeds document chunks (with PassagePrefix when set).
func (e *TEIEmbedder) EmbedPassages(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	inputs := make([]string, len(texts))
	for i, t := range texts {
		inputs[i] = e.PassagePrefix + t
	}
	return e.embed(ctx, inputs)
}

func (e *TEIEmbedder) embed(ctx context.Context, inputs []string) ([][]float32, error) {
	base, err := url.Parse(strings.TrimRight(e.BaseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("tei: invalid base URL")
	}
	endpoint := base.JoinPath("embed").String()
	body, err := json.Marshal(map[string]any{"inputs": inputs})
	if err != nil {
		return nil, fmt.Errorf("tei: marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tei: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client := e.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTEITimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tei: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	limited := io.LimitReader(resp.Body, maxTEIBody)
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, limited)
		return nil, fmt.Errorf("tei: status %d", resp.StatusCode)
	}
	var vectors [][]float32
	if err := json.NewDecoder(limited).Decode(&vectors); err != nil {
		return nil, fmt.Errorf("tei: decode: %w", err)
	}
	if len(vectors) != len(inputs) {
		return nil, fmt.Errorf("tei: got %d vectors for %d inputs", len(vectors), len(inputs))
	}
	return vectors, nil
}
