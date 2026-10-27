package wiring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	membershipadapter "birge/internal/domains/membership/adapter"
	membershipservice "birge/internal/domains/membership/service"
	sharingadapter "birge/internal/domains/sharing/adapter"
	"birge/internal/domains/sharing/imageproc"
	sharingmodel "birge/internal/domains/sharing/model"
	sharingservice "birge/internal/domains/sharing/service"
	"birge/internal/domains/sharing/sharingtest"
	"birge/internal/platform/clock"
	platformdb "birge/internal/platform/db"
	"birge/internal/platform/objectstore"
	"birge/internal/platform/testinfra"
)

const (
	e2eAuthorToken    = "tok-e2e-author-aaaaaaaa"
	e2eRequesterToken = "tok-e2e-requester-bbbbbb"
)

func (e *e2e) handlerWith(cfg Config, nc *nats.Conn, objects objectstore.Store) http.Handler {
	e.t.Helper()
	h, err := NewAPIHandler(context.Background(), APIDeps{
		Config: cfg, Pool: e.pool, Envelope: e.envelope,
		Logger: slog.New(slog.NewJSONHandler(e.logs, nil)),
		NATS:   nc, Objects: objects,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return h
}

func (e *e2e) verifyMember(ctx context.Context, memberID, districtID uuid.UUID) {
	e.t.Helper()
	if _, err := e.pool.Exec(ctx, `UPDATE membership.members SET verification = 'verified', district_id = $2 WHERE id = $1`,
		memberID, districtID); err != nil {
		e.t.Fatal(err)
	}
}

func TestChatWebSocketSendReceive(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	natsURL := testinfra.NATS(t)
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	h := e.handlerWith(e.cfg, nc, objectstore.Disabled{})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	rec := do(t, h, http.MethodGet, "/v1/me", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	var districtID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM membership.districts LIMIT 1`).Scan(&districtID); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodPatch, "/v1/me", e2eToken,
		fmt.Sprintf(`{"displayName":"Example","lang":"ru","districtId":%q}`, districtID)); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	e.verifyMember(ctx, me.ID, districtID)

	rec = do(t, h, http.MethodGet, "/v1/chat/rooms?joined=false", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("rooms: %d %s", rec.Code, rec.Body)
	}
	var rooms []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rooms); err != nil || len(rooms) == 0 {
		t.Fatalf("rooms body: %s %v", rec.Body, err)
	}
	roomID := rooms[0].ID
	if rec := do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/join", e2eToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/chat/ws"
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ws, resp, err := websocket.Dial(dctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	ws.SetReadLimit(1 << 20)

	clientID := uuid.Must(uuid.NewV7())
	writeWS(t, ws, map[string]any{"type": "auth", "token": e2eToken})
	writeWS(t, ws, map[string]any{
		"type": "subscribe", "rooms": []map[string]any{{"roomId": roomID.String(), "afterSeq": 0}},
	})
	writeWS(t, ws, map[string]any{
		"type": "send", "roomId": roomID.String(), "clientId": clientID.String(), "text": "hello from example chat",
	})

	gotAck, gotMsg := false, false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && (!gotAck || !gotMsg) {
		frame := readWS(t, ws, time.Until(deadline))
		switch frame["type"] {
		case "ack":
			if frame["clientId"] == clientID.String() {
				gotAck = true
			}
		case "message":
			if frame["text"] == "hello from example chat" {
				gotMsg = true
			}
		}
	}
	if !gotAck || !gotMsg {
		t.Fatalf("ack=%v msg=%v", gotAck, gotMsg)
	}
}

func TestChatCrisisHTTPReturnsPhonesAndEscalation(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	h := e.handlerWith(e.cfg, nil, objectstore.Disabled{})

	rec := do(t, h, http.MethodGet, "/v1/me", e2eToken, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("me: %d %s", rec.Code, rec.Body)
	}
	var me struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	var districtID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM membership.districts LIMIT 1`).Scan(&districtID); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodPatch, "/v1/me", e2eToken,
		fmt.Sprintf(`{"displayName":"Example","lang":"ru","districtId":%q}`, districtID)); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	e.verifyMember(ctx, me.ID, districtID)

	rec = do(t, h, http.MethodGet, "/v1/chat/rooms?joined=false", e2eToken, "")
	var rooms []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rooms); err != nil || len(rooms) == 0 {
		t.Fatalf("rooms: %s", rec.Body)
	}
	roomID := rooms[0].ID
	if rec := do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/join", e2eToken, ""); rec.Code != http.StatusOK {
		t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}

	clientID := uuid.Must(uuid.NewV7())
	body := fmt.Sprintf(`{"clientId":%q,"text":"хочу умереть"}`, clientID)
	rec = do(t, h, http.MethodPost, "/v1/chat/rooms/"+roomID.String()+"/messages", e2eToken, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	var msg struct {
		Status       string `json:"status"`
		CrisisPhones []struct {
			Number string `json:"number"`
			Label  string `json:"label"`
		} `json:"crisisPhones"`
		EscalationID *uuid.UUID `json:"escalationId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Status != "held" || len(msg.CrisisPhones) == 0 || msg.CrisisPhones[0].Number == "" {
		t.Fatalf("crisis response: %+v body=%s", msg, rec.Body)
	}
	if msg.EscalationID == nil || *msg.EscalationID == uuid.Nil {
		t.Fatalf("missing escalationId: %s", rec.Body)
	}
	var source string
	if err := e.pool.QueryRow(ctx, `SELECT source FROM safety.escalations WHERE id = $1`, *msg.EscalationID).Scan(&source); err != nil || source != "chat" {
		t.Fatalf("escalation source = %q, %v; want chat", source, err)
	}
	for _, secret := range []string{"хочу умереть", e2eToken} {
		if strings.Contains(e.logs.String(), secret) {
			t.Errorf("logs contain %q", secret)
		}
	}
}

func TestListingContactOpensDirectChat(t *testing.T) {
	ctx := context.Background()
	url := migratedDatabase(t)
	pool, err := platformdb.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	authorID, requesterID := uuid.New(), uuid.New()
	kratos := multiKratos(t, map[string]uuid.UUID{
		e2eAuthorToken: authorID, e2eRequesterToken: requesterID,
	})
	natsURL := testinfra.NATS(t)

	env := validEnv(t)
	env["DATABASE_URL"] = url
	env["KRATOS_PUBLIC_URL"] = kratos.URL
	env["KRATOS_ADMIN_URL"] = kratos.URL
	env["NATS_URL"] = natsURL
	env["BIRGE_DELIVERY_MODE"] = DeliveryModeDev
	env["BIRGE_HTTP_ADDR"] = "127.0.0.1:0"
	files := validFiles(t)
	apiCfg, err := load(env, files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	workerCfg, err := load(env, files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := NewEnvelope(pool, apiCfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	logs := &syncBuffer{}
	h, err := NewAPIHandler(ctx, APIDeps{
		Config: apiCfg, Pool: pool, Envelope: envelope,
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		NATS:   nc, Objects: objectstore.Disabled{},
	})
	if err != nil {
		t.Fatal(err)
	}

	var districtID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM membership.districts LIMIT 1`).Scan(&districtID); err != nil {
		t.Fatal(err)
	}
	members := membershipservice.New(&membershipadapter.Postgres{Pool: pool}, DisplayNameEncryptor{Sealer: envelope}, clock.System{})
	var authorMember, requesterMember uuid.UUID
	for _, tok := range []string{e2eAuthorToken, e2eRequesterToken} {
		rec := do(t, h, http.MethodGet, "/v1/me", tok, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("me %s: %d %s", tok, rec.Code, rec.Body)
		}
		var me struct {
			ID uuid.UUID `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &me)
		name := "Author"
		if tok == e2eRequesterToken {
			name = "Requester"
			requesterMember = me.ID
		} else {
			authorMember = me.ID
		}
		if rec := do(t, h, http.MethodPatch, "/v1/me", tok,
			fmt.Sprintf(`{"displayName":%q,"lang":"ru","districtId":%q}`, name, districtID)); rec.Code != http.StatusOK {
			t.Fatalf("patch: %d %s", rec.Code, rec.Body)
		}
		if _, err := pool.Exec(ctx, `UPDATE membership.members SET verification = 'verified', district_id = $2 WHERE id = $1`,
			me.ID, districtID); err != nil {
			t.Fatal(err)
		}
	}

	listingBody := fmt.Sprintf(
		`{"type":"give","category":"clothes","title":"Example coat","description":"Example description for listing","districtId":%q,"acceptRules":true}`,
		districtID)
	rec := do(t, h, http.MethodPost, "/v1/listings", e2eAuthorToken, listingBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create listing: %d %s", rec.Code, rec.Body)
	}
	var listing struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	sharing := &sharingservice.Service{
		Store:   &sharingadapter.PostgresStore{Pool: pool},
		Members: &SharingMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Objects: objectstore.Disabled{}, Images: imageproc.Processor{},
		Clock: clock.System{}, Logger: slog.New(slog.DiscardHandler),
	}
	if err := sharing.ApplyModerationDecided(ctx, uuid.New(), sharingmodel.ModerationDecidedData{
		ItemID: uuid.New(), Kind: sharingmodel.ModerationKindListing, ObjectID: listing.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}

	if rec := do(t, h, http.MethodPost, "/v1/listings/"+listing.ID.String()+"/contact-requests", e2eRequesterToken, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("contact: %d %s", rec.Code, rec.Body)
	}
	var contactID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM sharing.contact_requests WHERE listing_id = $1 AND requester_id = $2`,
		listing.ID, requesterMember).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodPost, "/v1/me/listing-contact-requests/"+contactID.String()+"/accept", e2eAuthorToken, ""); rec.Code != http.StatusAccepted {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}

	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RunWorker(wctx, slog.New(slog.DiscardHandler), workerCfg) }()

	waitFor(t, done, 20*time.Second, func() bool {
		var n int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM chat.rooms WHERE kind = 'direct' AND origin = 'sharing.listing' AND origin_id = $1`,
			listing.ID).Scan(&n)
		return err == nil && n == 1
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("worker did not stop")
	}

	var roomMembers int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM chat.room_members rm
		JOIN chat.rooms r ON r.id = rm.room_id
		WHERE r.origin = 'sharing.listing' AND r.origin_id = $1
		  AND rm.member_id IN ($2, $3) AND rm.left_at IS NULL`,
		listing.ID, authorMember, requesterMember).Scan(&roomMembers); err != nil || roomMembers != 2 {
		t.Fatalf("direct room members = %d, %v", roomMembers, err)
	}
}

func TestPhotoUploadFakeStoreAndProcess(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	objects := sharingtest.NewMemoryObjects()
	h := e.handlerWith(e.cfg, nil, objects)

	rec := do(t, h, http.MethodGet, "/v1/me", e2eToken, "")
	var me struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	var districtID uuid.UUID
	if err := e.pool.QueryRow(ctx, `SELECT id FROM membership.districts LIMIT 1`).Scan(&districtID); err != nil {
		t.Fatal(err)
	}
	if rec := do(t, h, http.MethodPatch, "/v1/me", e2eToken,
		fmt.Sprintf(`{"displayName":"Example","lang":"ru","districtId":%q}`, districtID)); rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	e.verifyMember(ctx, me.ID, districtID)

	rec = do(t, h, http.MethodPost, "/v1/listings/photo-uploads", e2eToken,
		`{"contentType":"image/jpeg","sizeBytes":1024}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("photo-upload: %d %s", rec.Code, rec.Body)
	}
	var slot struct {
		UploadID uuid.UUID `json:"uploadId"`
		URL      string    `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &slot); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(slot.URL, "https://objects.example.invalid/put/sharing/uploads/") {
		t.Fatalf("url shape: %s", slot.URL)
	}
	raw := jpegWithEXIFMarker(t)
	if err := objects.Put(ctx, sharingmodel.UploadObjectKey(slot.UploadID), raw, sharingmodel.ContentJPEG); err != nil {
		t.Fatal(err)
	}
	listingBody := fmt.Sprintf(
		`{"type":"give","category":"clothes","title":"Example coat","description":"Example description for listing","districtId":%q,"photoUploadId":%q,"acceptRules":true}`,
		districtID, slot.UploadID)
	rec = do(t, h, http.MethodPost, "/v1/listings", e2eToken, listingBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("listing: %d %s", rec.Code, rec.Body)
	}
	var listing struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &listing)

	members := membershipservice.New(&membershipadapter.Postgres{Pool: e.pool}, DisplayNameEncryptor{Sealer: e.envelope}, clock.System{})
	sharing := &sharingservice.Service{
		Store:   &sharingadapter.PostgresStore{Pool: e.pool},
		Members: &SharingMembers{Members: members},
		Blocks:  &EmergencyBlocks{Members: members},
		Objects: objects, Images: imageproc.Processor{},
		Clock: clock.System{}, Logger: slog.New(slog.DiscardHandler),
	}
	if err := sharing.ApplyModerationDecided(ctx, uuid.New(), sharingmodel.ModerationDecidedData{
		ItemID: uuid.New(), Kind: sharingmodel.ModerationKindListing, ObjectID: listing.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sharing.HandleProcessPhoto(ctx, listing.ID); err != nil {
		t.Fatal(err)
	}
	var photoKey *string
	if err := e.pool.QueryRow(ctx, `SELECT photo_object_key FROM sharing.listings WHERE id = $1`, listing.ID).Scan(&photoKey); err != nil {
		t.Fatal(err)
	}
	if photoKey == nil {
		t.Fatal("missing processed key")
	}
	processed, err := objects.Get(ctx, *photoKey)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(processed, []byte("Exif")) {
		t.Fatal("processed photo still carries EXIF marker")
	}
	if !bytes.HasPrefix(processed, []byte{0xff, 0xd8}) {
		t.Fatal("processed photo is not JPEG")
	}
}

func TestPhotoUploadStorageNotConfigured(t *testing.T) {
	e := newE2E(t)
	h := e.handlerWith(e.cfg, nil, objectstore.Disabled{})
	rec := do(t, h, http.MethodGet, "/v1/me", e2eToken, "")
	var me struct {
		ID uuid.UUID `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	var districtID uuid.UUID
	_ = e.pool.QueryRow(context.Background(), `SELECT id FROM membership.districts LIMIT 1`).Scan(&districtID)
	_ = do(t, h, http.MethodPatch, "/v1/me", e2eToken,
		fmt.Sprintf(`{"displayName":"Example","lang":"ru","districtId":%q}`, districtID))
	e.verifyMember(context.Background(), me.ID, districtID)

	rec = do(t, h, http.MethodPost, "/v1/listings/photo-uploads", e2eToken,
		`{"contentType":"image/jpeg","sizeBytes":1024}`)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "storage_not_configured") {
		t.Fatalf("want 503 storage_not_configured, got %d %s", rec.Code, rec.Body)
	}
}

func writeWS(t *testing.T, ws *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func readWS(t *testing.T, ws *websocket.Conn, wait time.Duration) map[string]any {
	t.Helper()
	if wait <= 0 {
		wait = time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("ws frame: %s %v", data, err)
	}
	return m
}

// jpegWithEXIFMarker builds a tiny JPEG and injects an APP1/Exif marker so
// stripping can be asserted without a real camera file in the repository.
func jpegWithEXIFMarker(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	app1 := []byte{0xff, 0xe1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 0, 0, 0, 0, 0, 0}
	out := make([]byte, 0, len(raw)+len(app1))
	out = append(out, raw[:2]...)
	out = append(out, app1...)
	out = append(out, raw[2:]...)
	return out
}
