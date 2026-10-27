package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	circleadapter "birge/internal/domains/circle/adapter"
	circleservice "birge/internal/domains/circle/service"
	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/retrieval"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipservice "birge/internal/domains/membership/service"
	"birge/internal/platform/clock"
	platformdb "birge/internal/platform/db"
)

// fakeMessagesAPI records Anthropic Messages requests (crisis must never arrive).
type fakeMessagesAPI struct {
	mu       sync.Mutex
	requests []string
	reply    string
}

func (f *fakeMessagesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, string(body))
	text := f.reply
	f.mu.Unlock()
	if text == "" {
		text = `{"reply":"DEMO answer about benefits.","crisis":false,"sources":[],"suggest":"none"}`
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "msg_e2e", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "", "signature": "sig"},
			map[string]any{"type": "text", "text": text},
		},
		"stop_reason": "end_turn",
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
}

func (f *fakeMessagesAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeMessagesAPI) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

type fakeTEIE2E struct{}

func (f *fakeTEIE2E) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/embed" {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Inputs []string `json:"inputs"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	out := make([][]float32, len(req.Inputs))
	for i, in := range req.Inputs {
		v := []float32{0.1, 0.1, 0.1, 0.1}
		low := strings.ToLower(in)
		if strings.Contains(low, "пособи") || strings.Contains(low, "benefit") || strings.Contains(low, "инвалид") {
			v[2] = 1
		} else {
			v[3] = 1
		}
		out[i] = v
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

type fakeQdrantE2E struct {
	mu     sync.Mutex
	points map[string]retrieval.Point
}

func (f *fakeQdrantE2E) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/collections/"):
		f.mu.Lock()
		ok := f.points != nil
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "green"}})
	case r.Method == http.MethodPut && strings.HasPrefix(path, "/collections/") && !strings.Contains(path, "/points"):
		f.mu.Lock()
		if f.points == nil {
			f.points = map[string]retrieval.Point{}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true})
	case r.Method == http.MethodPost && path == "/collections/aliases":
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
			f.points[p.ID] = retrieval.Point{ID: uuid.MustParse(p.ID), Vector: p.Vector, Payload: p.Payload}
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"status": "ok"}})
	case r.Method == http.MethodPost && strings.Contains(path, "/points/search"):
		var req struct {
			Vector         []float32 `json:"vector"`
			Limit          int       `json:"limit"`
			ScoreThreshold float32   `json:"score_threshold"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		type scored struct {
			score float32
			p     retrieval.Point
		}
		var ranked []scored
		f.mu.Lock()
		for _, p := range f.points {
			var s float32
			n := len(req.Vector)
			if len(p.Vector) < n {
				n = len(p.Vector)
			}
			for i := 0; i < n; i++ {
				s += req.Vector[i] * p.Vector[i]
			}
			if s >= req.ScoreThreshold {
				ranked = append(ranked, scored{s, p})
			}
		}
		f.mu.Unlock()
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

type oneArticle struct{ a retrieval.Article }

func (m *oneArticle) GetPublished(_ context.Context, id uuid.UUID) (retrieval.Article, error) {
	if m.a.VersionID == id {
		return m.a, nil
	}
	return retrieval.Article{}, model.ErrNotFound
}
func (m *oneArticle) ListPublished(context.Context) ([]retrieval.Article, error) {
	return []retrieval.Article{m.a}, nil
}

func TestAssistantRetrievalAndCrisisNeverHitsModel(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()

	llm := &fakeMessagesAPI{}
	llmSrv := httptest.NewServer(llm)
	t.Cleanup(llmSrv.Close)
	teiSrv := httptest.NewServer(&fakeTEIE2E{})
	t.Cleanup(teiSrv.Close)
	qdSrv := httptest.NewServer(&fakeQdrantE2E{})
	t.Cleanup(qdSrv.Close)

	e.files["/keys/llm-api-key"] = []byte("test-key-not-a-secret\n")
	env := e.env
	env["BIRGE_LLM_API_KEY_FILE"] = "/keys/llm-api-key"
	env["BIRGE_LLM_BASE_URL"] = llmSrv.URL
	env["BIRGE_TEI_URL"] = teiSrv.URL
	env["BIRGE_QDRANT_URL"] = qdSrv.URL
	env["BIRGE_EMBEDDING_MODEL_ID"] = "test/e5"
	env["BIRGE_QDRANT_SCORE_THRESHOLD"] = "0.01"
	cfg, err := load(env, e.files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}

	var articleID, versionID uuid.UUID
	var title, body string
	err = e.pool.QueryRow(ctx, `
		SELECT a.id, v.id, v.title, v.body FROM knowledge.versions v
		JOIN knowledge.articles a ON a.id = v.article_id
		WHERE v.lang = 'ru' AND a.kind = 'rights' AND v.status = 'published'
		ORDER BY v.created_at LIMIT 1`).Scan(&articleID, &versionID, &title, &body)
	if err != nil {
		t.Fatalf("demo knowledge: %v", err)
	}
	indexer := &retrieval.Indexer{
		Articles: &oneArticle{a: retrieval.Article{
			ArticleID: articleID, VersionID: versionID, Lang: "ru", Kind: "rights",
			Title: title, SourceURL: "https://example.invalid/demo",
			Body: body + " пособие benefits",
		}},
		Embedder: &retrieval.TEIEmbedder{
			BaseURL: teiSrv.URL, QueryPrefix: "query: ", PassagePrefix: "passage: ",
			HTTPClient: teiSrv.Client(),
		},
		Index: &retrieval.QdrantIndex{
			BaseURL: qdSrv.URL, Collection: retrieval.CollectionName("birge-kb", "test/e5"),
			Alias: "birge-kb", HTTPClient: qdSrv.Client(),
		},
	}
	if err := indexer.IndexVersion(ctx, versionID); err != nil {
		t.Fatal(err)
	}
	llm.reply = fmt.Sprintf(
		`{"reply":"DEMO: about the benefit.","crisis":false,"sources":[%q],"suggest":"none"}`,
		articleID.String())

	h := e.handler(cfg)
	if rec := do(t, h, http.MethodGet, "/v1/me", e2eToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPatch, "/v1/me", e2eToken, `{"displayName":"Example Name","lang":"ru"}`); rec.Code != http.StatusOK {
		t.Fatalf("patch me: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPost, "/v1/me/consents", e2eToken,
		`{"type":"ai_crossborder","version":"v1","granted":true}`); rec.Code != http.StatusOK {
		t.Fatalf("consent: %d %s", rec.Code, rec.Body)
	}

	rec := do(t, h, http.MethodPost, "/v1/me/assistant/messages", e2eToken,
		`{"text":"Какое пособие по инвалидности?","lang":"ru","scope":"rights"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("assistant: %d %s", rec.Code, rec.Body)
	}
	var reply struct {
		Crisis   bool `json:"crisis"`
		Degraded bool `json:"degraded"`
		Sources  []struct {
			ArticleID uuid.UUID `json:"articleId"`
		} `json:"sources"`
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Crisis || reply.Degraded || reply.Text == nil || len(reply.Sources) == 0 {
		t.Fatalf("reply = %+v body=%s", reply, rec.Body)
	}
	if reply.Sources[0].ArticleID != articleID {
		t.Fatalf("source = %s, want %s", reply.Sources[0].ArticleID, articleID)
	}
	if llm.count() != 1 {
		t.Fatalf("llm calls = %d, want 1", llm.count())
	}

	before := llm.count()
	rec = do(t, h, http.MethodPost, "/v1/me/assistant/messages", e2eToken,
		`{"text":"хочу умереть","lang":"ru","scope":"general"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("crisis: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	if !reply.Crisis {
		t.Fatalf("expected crisis: %s", rec.Body)
	}
	if llm.count() != before {
		t.Fatalf("crisis reached the model (%d calls): %v", llm.count(), llm.bodies())
	}
	for _, b := range llm.bodies() {
		if strings.Contains(b, "хочу умереть") {
			t.Fatalf("crisis text in model request")
		}
	}
}

func multiKratos(t *testing.T, tokens map[string]uuid.UUID) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sessions/whoami", func(w http.ResponseWriter, r *http.Request) {
		id, ok := tokens[r.Header.Get("X-Session-Token")]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"active":true,"expires_at":%q,"authenticator_assurance_level":"aal1","identity":{"id":%q}}`,
			time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), id)
	})
	mux.HandleFunc("GET /admin/identities/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"traits":{"phone":%q}}`, r.PathValue("id"), testPhone)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCircleMatchToTrustedContact(t *testing.T) {
	ctx := context.Background()
	url := migratedDatabase(t)
	pool, err := platformdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	idA, idB := uuid.New(), uuid.New()
	tokA, tokB := "tok-circle-a-000000000000", "tok-circle-b-000000000000"
	kratos := multiKratos(t, map[string]uuid.UUID{tokA: idA, tokB: idB})
	staffIdP := fakeStaffIdP(t)

	env := validEnv(t)
	env["DATABASE_URL"] = url
	env["KRATOS_PUBLIC_URL"] = kratos.URL
	env["KRATOS_ADMIN_URL"] = kratos.URL
	env["BIRGE_MIN_APP_VERSION"] = "1.0.0"
	env["BIRGE_STAFF_INTROSPECTION_URL"] = staffIdP.URL + "/admin/oauth2/introspect"
	env["BIRGE_STAFF_CLIENT_ID"] = e2eStaffClientID
	env["BIRGE_STAFF_CLIENT_SECRET_FILE"] = "/keys/staff-client-secret"
	env["BIRGE_STAFF_ISSUER"] = e2eStaffIssuer
	env["BIRGE_STAFF_AUDIENCE"] = e2eStaffClientID
	env["BIRGE_STAFF_ROLES_CLAIM"] = "ext.roles"
	env["BIRGE_STAFF_MFA_AMR"] = "webauthn,hwk"
	files := validFiles(t)
	files["/keys/staff-client-secret"] = []byte(e2eStaffSecret + "\n")
	cfg, err := load(env, files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := NewEnvelope(pool, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewAPIHandler(ctx, APIDeps{
		Config: cfg, Pool: pool, Envelope: envelope,
		Logger: slog.New(slog.NewJSONHandler(&syncBuffer{}, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}

	var districtID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM membership.districts LIMIT 1`).Scan(&districtID); err != nil {
		t.Fatalf("district: %v", err)
	}
	var memberA, memberB uuid.UUID
	for i, tok := range []string{tokA, tokB} {
		rec := do(t, h, http.MethodGet, "/v1/me", tok, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("me: %d %s", rec.Code, rec.Body)
		}
		var me struct {
			ID uuid.UUID `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			memberA = me.ID
		} else {
			memberB = me.ID
		}
		name := "ExampleA"
		if i == 1 {
			name = "ExampleB"
		}
		rec = do(t, h, http.MethodPatch, "/v1/me", tok,
			fmt.Sprintf(`{"displayName":%q,"lang":"ru","districtId":%q}`, name, districtID))
		if rec.Code != http.StatusOK {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body)
		}
		rec = do(t, h, http.MethodPost, "/v1/me/children", tok, `{"birthYear":2019,"needs":["autism"]}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("child: %d %s", rec.Code, rec.Body)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE membership.members SET verification = 'verified' WHERE id = ANY($1)`,
		[]uuid.UUID{memberA, memberB}); err != nil {
		t.Fatal(err)
	}

	for _, tok := range []string{tokA, tokB} {
		rec := do(t, h, http.MethodPost, "/v1/me/tea-requests", tok,
			`{"format":"meeting","sameDistrict":true,"similarAge":false,"sharedNeeds":false}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("tea: %d %s", rec.Code, rec.Body)
		}
	}

	members := membershipservice.New(&membershipadapter.Postgres{Pool: pool}, DisplayNameEncryptor{Sealer: envelope}, clock.System{})
	circle := &circleservice.Service{
		Store:   &circleadapter.PostgresStore{Pool: pool},
		Members: &CircleMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Clock:   clock.System{},
		Logger:  slog.New(slog.DiscardHandler),
	}
	err = platformdb.InTx(ctx, pool, func(tx pgx.Tx) error {
		return circle.HandleMatchRun(ctx, circleadapter.Bind(tx))
	})
	if err != nil {
		t.Fatalf("match run: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/v1/me/matches", tokA, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("matches: %d %s", rec.Code, rec.Body)
	}
	var matchList []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &matchList); err != nil || len(matchList) == 0 {
		t.Fatalf("matches body: %s (%v)", rec.Body, err)
	}
	matchID := matchList[0].ID
	for _, tok := range []string{tokA, tokB} {
		rec := do(t, h, http.MethodPost, "/v1/me/matches/"+matchID.String()+"/answer", tok, `{"accept":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("answer: %d %s", rec.Code, rec.Body)
		}
	}
	for _, tok := range []string{tokA, tokB} {
		rec := do(t, h, http.MethodPost, "/v1/me/matches/"+matchID.String()+"/met", tok, `{"met":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("met: %d %s", rec.Code, rec.Body)
		}
	}
	met, err := circle.Met(ctx, memberA, memberB)
	if err != nil || !met {
		t.Fatalf("Met = %v, %v", met, err)
	}

	rec = do(t, h, http.MethodPost, "/v1/me/trusted-contacts", tokA,
		fmt.Sprintf(`{"kind":"member","memberId":%q}`, memberB))
	if rec.Code != http.StatusCreated {
		t.Fatalf("trusted contact: %d %s (want 201; NeverMet would be 403)", rec.Code, rec.Body)
	}
}
