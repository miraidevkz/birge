package retrieval_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/retrieval"
)

// fakeTEI emulates POST /embed → [[float,...], ...].
type fakeTEI struct {
	mu       sync.Mutex
	inputs   [][]string
	dim      int
	failNext bool
}

func (f *fakeTEI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/embed" {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var req struct {
		Inputs []string `json:"inputs"`
	}
	_ = json.Unmarshal(body, &req)
	f.mu.Lock()
	f.inputs = append(f.inputs, append([]string(nil), req.Inputs...))
	fail := f.failNext
	f.failNext = false
	dim := f.dim
	f.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if dim == 0 {
		dim = 4
	}
	out := make([][]float32, len(req.Inputs))
	for i, in := range req.Inputs {
		v := make([]float32, dim)
		v[0] = float32(len(in))
		v[1] = float32(i + 1)
		// Distinct direction per input so search can prefer a match.
		if strings.Contains(in, "rights") {
			v[2] = 1
		} else {
			v[3] = 1
		}
		out[i] = v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// fakeQdrant is an in-memory Qdrant REST stub.
type fakeQdrant struct {
	mu         sync.Mutex
	collection string
	alias      string
	points     map[string]retrieval.Point
	created    bool
}

func (f *fakeQdrant) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/collections/") && !strings.Contains(path, "/points"):
		name := strings.TrimPrefix(path, "/collections/")
		f.mu.Lock()
		ok := f.created && name == f.collection
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status":{"error":"not found"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "green"}})
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/collections/") && !strings.Contains(path, "/points"):
		name := strings.TrimPrefix(path, "/collections/")
		f.mu.Lock()
		f.collection = name
		f.created = true
		if f.points == nil {
			f.points = map[string]retrieval.Point{}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
	case r.Method == http.MethodPost && path == "/collections/aliases":
		f.mu.Lock()
		f.alias = f.collection
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
	case r.Method == http.MethodPut && strings.Contains(path, "/points"):
		var req struct {
			Points []struct {
				ID      string                 `json:"id"`
				Vector  []float32              `json:"vector"`
				Payload retrieval.PointPayload `json:"payload"`
			} `json:"points"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		if f.points == nil {
			f.points = map[string]retrieval.Point{}
		}
		for _, p := range req.Points {
			id := uuid.MustParse(p.ID)
			f.points[p.ID] = retrieval.Point{ID: id, Vector: p.Vector, Payload: p.Payload}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
	case r.Method == http.MethodPost && strings.Contains(path, "/points/delete"):
		var req struct {
			Filter struct {
				Must []struct {
					Key   string `json:"key"`
					Match struct {
						Value string `json:"value"`
					} `json:"match"`
				} `json:"must"`
			} `json:"filter"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var version string
		for _, m := range req.Filter.Must {
			if m.Key == "version_id" {
				version = m.Match.Value
			}
		}
		f.mu.Lock()
		for id, p := range f.points {
			if p.Payload.VersionID.String() == version {
				delete(f.points, id)
			}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
	case r.Method == http.MethodPost && strings.Contains(path, "/points/search"):
		var req struct {
			Vector []float32 `json:"vector"`
			Limit  int       `json:"limit"`
			Filter struct {
				Must []json.RawMessage `json:"must"`
			} `json:"filter"`
			ScoreThreshold float32 `json:"score_threshold"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		lang, kinds := parseSearchFilter(req.Filter.Must)
		type scored struct {
			score float32
			p     retrieval.Point
		}
		var ranked []scored
		f.mu.Lock()
		for _, p := range f.points {
			if p.Payload.Lang != lang {
				continue
			}
			if len(kinds) > 0 && !kindOK(p.Payload.Kind, kinds) {
				continue
			}
			score := dot(req.Vector, p.Vector)
			if score < req.ScoreThreshold {
				continue
			}
			ranked = append(ranked, scored{score, p})
		}
		f.mu.Unlock()
		// Simple selection sort by score desc.
		for i := 0; i < len(ranked); i++ {
			best := i
			for j := i + 1; j < len(ranked); j++ {
				if ranked[j].score > ranked[best].score {
					best = j
				}
			}
			ranked[i], ranked[best] = ranked[best], ranked[i]
		}
		if req.Limit > 0 && len(ranked) > req.Limit {
			ranked = ranked[:req.Limit]
		}
		result := make([]map[string]any, 0, len(ranked))
		for _, r := range ranked {
			result = append(result, map[string]any{"score": r.score, "payload": r.p.Payload})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	default:
		http.NotFound(w, r)
	}
}

func parseSearchFilter(must []json.RawMessage) (lang string, kinds []string) {
	for _, raw := range must {
		var plain struct {
			Key   string `json:"key"`
			Match struct {
				Value string `json:"value"`
			} `json:"match"`
			Should []struct {
				Key   string `json:"key"`
				Match struct {
					Value string `json:"value"`
				} `json:"match"`
			} `json:"should"`
		}
		_ = json.Unmarshal(raw, &plain)
		if plain.Key == "lang" {
			lang = plain.Match.Value
		}
		if plain.Key == "kind" {
			kinds = append(kinds, plain.Match.Value)
		}
		for _, s := range plain.Should {
			if s.Key == "kind" {
				kinds = append(kinds, s.Match.Value)
			}
		}
	}
	return lang, kinds
}

func kindOK(kind string, kinds []string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func dot(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var s float32
	for i := 0; i < n; i++ {
		s += a[i] * b[i]
	}
	return s
}

func TestTEIEmbedderPrefixes(t *testing.T) {
	api := &fakeTEI{}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	emb := &retrieval.TEIEmbedder{
		BaseURL: srv.URL, QueryPrefix: "query: ", PassagePrefix: "passage: ",
		HTTPClient: srv.Client(),
	}
	ctx := context.Background()
	if _, err := emb.EmbedQuery(ctx, "hello"); err != nil {
		t.Fatal(err)
	}
	vecs, err := emb.EmbedPassages(ctx, []string{"doc one", "doc two"})
	if err != nil || len(vecs) != 2 {
		t.Fatalf("passages: %v %v", vecs, err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.inputs) != 2 {
		t.Fatalf("calls = %d", len(api.inputs))
	}
	if api.inputs[0][0] != "query: hello" {
		t.Errorf("query input = %q", api.inputs[0][0])
	}
	if api.inputs[1][0] != "passage: doc one" || api.inputs[1][1] != "passage: doc two" {
		t.Errorf("passage inputs = %#v", api.inputs[1])
	}
}

func TestQdrantIndexUpsertSearchDelete(t *testing.T) {
	fq := &fakeQdrant{}
	srv := httptest.NewServer(fq)
	t.Cleanup(srv.Close)
	idx := &retrieval.QdrantIndex{
		BaseURL: srv.URL, Collection: "birge-kb--test-model", Alias: "birge-kb",
		HTTPClient: srv.Client(),
	}
	ctx := context.Background()
	aid, vid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	p := retrieval.Point{
		ID: uuid.Must(uuid.NewV7()), Vector: []float32{0.1, 0.2, 1, 0},
		Payload: retrieval.PointPayload{
			ArticleID: aid, VersionID: vid, Lang: model.LangRU, Kind: model.KindRights,
			Title: "DEMO rights", Text: "passage about rights", SourceURL: "https://example.invalid/law",
		},
	}
	if err := idx.Upsert(ctx, []retrieval.Point{p}); err != nil {
		t.Fatal(err)
	}
	hits, err := idx.Search(ctx, []float32{0, 0, 1, 0}, model.LangRU, []string{model.KindRights}, 5, 0.01)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search: %v %v", hits, err)
	}
	if hits[0].Payload.ArticleID != aid || hits[0].Payload.Title != "DEMO rights" {
		t.Fatalf("hit = %+v", hits[0])
	}
	if err := idx.DeleteByVersion(ctx, vid); err != nil {
		t.Fatal(err)
	}
	hits, err = idx.Search(ctx, []float32{0, 0, 1, 0}, model.LangRU, []string{model.KindRights}, 5, 0.01)
	if err != nil || len(hits) != 0 {
		t.Fatalf("after delete: %v %v", hits, err)
	}
}

func TestIndexerAndRetriever(t *testing.T) {
	teiAPI := &fakeTEI{dim: 4}
	teiSrv := httptest.NewServer(teiAPI)
	t.Cleanup(teiSrv.Close)
	fq := &fakeQdrant{}
	qdSrv := httptest.NewServer(fq)
	t.Cleanup(qdSrv.Close)

	emb := &retrieval.TEIEmbedder{
		BaseURL: teiSrv.URL, QueryPrefix: "query: ", PassagePrefix: "passage: ",
		HTTPClient: teiSrv.Client(),
	}
	idx := &retrieval.QdrantIndex{
		BaseURL: qdSrv.URL, Collection: retrieval.CollectionName("birge-kb", "test/e5"),
		Alias: "birge-kb", HTTPClient: qdSrv.Client(),
	}
	aid, vid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	arts := &memArticles{items: []retrieval.Article{{
		ArticleID: aid, VersionID: vid, Lang: model.LangRU, Kind: model.KindRights,
		Title: "DEMO benefit", SourceURL: "https://example.invalid/a",
		Body: "This DEMO article explains disability rights and benefits for mothers.",
	}}}
	indexer := &retrieval.Indexer{Articles: arts, Embedder: emb, Index: idx}
	ctx := context.Background()
	if err := indexer.IndexVersion(ctx, vid); err != nil {
		t.Fatal(err)
	}
	ret := &retrieval.Retriever{Embedder: emb, Index: idx, ScoreThreshold: 0.01}
	hits, err := ret.Search(ctx, model.LangRU, "rights", "disability rights", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("retrieve: %v %v", hits, err)
	}
	if hits[0].ArticleID != aid || hits[0].Title != "DEMO benefit" {
		t.Fatalf("hit = %+v", hits[0])
	}
	// Idempotent re-index keeps a single article hit.
	if err := indexer.IndexVersion(ctx, vid); err != nil {
		t.Fatal(err)
	}
	hits, err = ret.Search(ctx, model.LangRU, "rights", "disability rights", 5)
	if err != nil || len(hits) != 1 {
		t.Fatalf("after reindex: %v %v", hits, err)
	}
	n, err := indexer.Reindex(ctx)
	if err != nil || n != 1 {
		t.Fatalf("reindex: %d %v", n, err)
	}
}

func TestNoRetrieval(t *testing.T) {
	hits, err := (retrieval.NoRetrieval{}).Search(context.Background(), "ru", "general", "x", 5)
	if err != nil || hits != nil {
		t.Fatalf("got %v %v", hits, err)
	}
}

func TestCollectionName(t *testing.T) {
	got := retrieval.CollectionName("birge-kb", "intfloat/multilingual-e5-base")
	if got != "birge-kb--intfloat_multilingual-e5-base" {
		t.Fatalf("got %q", got)
	}
}

type memArticles struct {
	items []retrieval.Article
}

func (m *memArticles) GetPublished(_ context.Context, versionID uuid.UUID) (retrieval.Article, error) {
	for _, a := range m.items {
		if a.VersionID == versionID {
			return a, nil
		}
	}
	return retrieval.Article{}, model.ErrNotFound
}

func (m *memArticles) ListPublished(context.Context) ([]retrieval.Article, error) {
	return append([]retrieval.Article(nil), m.items...), nil
}
