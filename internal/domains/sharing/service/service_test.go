package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/sharing/imageproc"
	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
	"birge/internal/domains/sharing/service"
	"birge/internal/domains/sharing/sharingtest"
	"birge/internal/platform/auth"
)

const fakeTitle = "Example stroller"

type env struct {
	svc     *service.Service
	store   *sharingtest.MemoryStore
	members *sharingtest.FakeMembers
	objects *sharingtest.MemoryObjects
	clock   *sharingtest.Clock
	logs    *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := sharingtest.NewMemoryStore()
	members := sharingtest.NewFakeMembers()
	objects := sharingtest.NewMemoryObjects()
	clk := &sharingtest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	var logs bytes.Buffer
	e := &env{store: store, members: members, objects: objects, clock: clk, logs: &logs}
	e.svc = &service.Service{
		Store: store, Members: members, Blocks: members, Objects: objects,
		Images: imageproc.Processor{}, Clock: clk,
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	t.Cleanup(func() {
		for _, bad := range []string{fakeTitle, "Example description"} {
			if strings.Contains(logs.String(), bad) {
				t.Errorf("log leaked content %q", bad)
			}
		}
		for _, ev := range store.Events() {
			raw, _ := json.Marshal(ev.Data)
			for _, bad := range []string{fakeTitle, "Example description"} {
				if strings.Contains(string(raw), bad) {
					t.Errorf("event %s leaked %q", ev.Type, bad)
				}
			}
		}
	})
	return e
}

func member(e *env, verified bool) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{Verified: verified})
	return p
}

func createListingPending(t *testing.T, e *env, author auth.Principal) model.ListingView {
	t.Helper()
	v, err := e.svc.CreateListing(context.Background(), author, uuid.Must(uuid.NewV7()), model.NewListing{
		Type: model.TypeGive, Category: "clothes", Title: fakeTitle,
		Description: "Example description for listing", DistrictID: uuid.New(), AcceptRules: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func activate(t *testing.T, e *env, id uuid.UUID) {
	t.Helper()
	if err := e.svc.ApplyModerationDecided(context.Background(), uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindListing, ObjectID: id, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
}

func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	app1 := []byte{0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}
	out := append([]byte{0xFF, 0xD8}, app1...)
	out = append(out, raw[2:]...)
	return out
}

func TestCreateListingRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true)
	key := uuid.Must(uuid.NewV7())
	in := model.NewListing{
		Type: model.TypeGive, Category: "clothes", Title: fakeTitle,
		Description: "Example description for listing", DistrictID: uuid.New(), AcceptRules: true,
	}
	v, err := e.svc.CreateListing(ctx, author, key, in)
	if err != nil || v.Status != model.ListingPending {
		t.Fatalf("create: %+v %v", v, err)
	}
	if len(e.store.EventsOfType(model.EventListingSubmitted)) != 1 {
		t.Fatal("expected ListingSubmitted")
	}
	v2, err := e.svc.CreateListing(ctx, author, key, in)
	if err != nil || v2.ID != v.ID {
		t.Fatalf("idempotent: %+v %v", v2, err)
	}
	if len(e.store.EventsOfType(model.EventListingSubmitted)) != 1 {
		t.Fatal("duplicate event")
	}
	if _, err := e.svc.CreateListing(ctx, member(e, false), uuid.New(), in); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unverified: %v", err)
	}
	bad := in
	bad.AcceptRules = false
	if _, err := e.svc.CreateListing(ctx, author, uuid.New(), bad); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("rules: %v", err)
	}
}

func TestFeedBlocksAndOwn(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true)
	viewer := member(e, true)
	blockedAuthor := member(e, true)
	e.members.Block(viewer.MemberID, blockedAuthor.MemberID)

	own := createListingPending(t, e, author)
	activate(t, e, own.ID)
	ownViewer := createListingPending(t, e, viewer)
	activate(t, e, ownViewer.ID)
	other := createListingPending(t, e, member(e, true))
	activate(t, e, other.ID)
	hid := createListingPending(t, e, blockedAuthor)
	activate(t, e, hid.ID)

	page, err := e.svc.ListListings(ctx, viewer, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]bool{}
	for _, it := range page.Items {
		seen[it.ID] = true
		if it.AuthorID == viewer.MemberID {
			t.Fatal("own listing in feed")
		}
	}
	if seen[ownViewer.ID] || seen[hid.ID] {
		t.Fatal("own or blocked listing visible")
	}
	if !seen[other.ID] || !seen[own.ID] {
		t.Fatal("expected visible listings missing")
	}
}

func TestContactRequestFlow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true)
	requester := member(e, true)
	listing := createListingPending(t, e, author)
	activate(t, e, listing.ID)

	if err := e.svc.RequestContact(ctx, requester, listing.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RequestContact(ctx, requester, listing.ID); err != nil {
		t.Fatal(err)
	}
	dels := 0
	var contactID uuid.UUID
	for _, ev := range e.store.Events() {
		if ev.Type != model.EventDeliveryRequested {
			continue
		}
		d := ev.Data.(model.DeliveryRequestedData)
		if d.Template != model.TemplateContactRequest {
			continue
		}
		dels++
		contactID = uuid.MustParse(d.Params["contact_request_id"])
	}
	if dels != 1 {
		t.Fatalf("contact_request deliveries=%d", dels)
	}
	if err := e.svc.AcceptContact(ctx, author, contactID); err != nil {
		t.Fatal(err)
	}
	if len(e.store.EventsOfType(model.EventContactAccepted)) != 1 {
		t.Fatal("expected ContactAccepted")
	}
}

func TestModerationApproveReject(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true)
	v := createListingPending(t, e, author)
	eventID := uuid.New()
	if err := e.svc.ApplyModerationDecided(ctx, eventID, model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindListing, ObjectID: v.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	l, _ := e.store.Listing(v.ID)
	if l.Status != model.ListingActive || l.ClosesAt == nil {
		t.Fatalf("not active: %+v", l)
	}
	if err := e.svc.ApplyModerationDecided(ctx, eventID, model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindListing, ObjectID: v.ID, Approve: false, Reason: "spam",
	}); err != nil {
		t.Fatal(err)
	}
	l, _ = e.store.Listing(v.ID)
	if l.Status != model.ListingActive {
		t.Fatal("dedup changed status")
	}

	v2 := createListingPending(t, e, author)
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindListing, ObjectID: v2.ID, Approve: false, Reason: "spam",
	}); err != nil {
		t.Fatal(err)
	}
	l2, _ := e.store.Listing(v2.ID)
	if l2.Status != model.ListingRejected || l2.RejectReason == nil || *l2.RejectReason != "spam" {
		t.Fatalf("reject: %+v", l2)
	}
}

func TestPhotoStaysPendingUntilProcessed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true)
	slot, err := e.svc.CreatePhotoUpload(ctx, author, model.ContentJPEG, 100)
	if err != nil {
		t.Fatal(err)
	}
	raw := jpegWithEXIF(t)
	if err := e.objects.Put(ctx, model.UploadObjectKey(slot.UploadID), raw, model.ContentJPEG); err != nil {
		t.Fatal(err)
	}
	v, err := e.svc.CreateListing(ctx, author, uuid.Must(uuid.NewV7()), model.NewListing{
		Type: model.TypeGive, Category: "toys", Title: fakeTitle,
		Description: "Example description for listing", DistrictID: uuid.New(),
		PhotoUploadID: &slot.UploadID, AcceptRules: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindListing, ObjectID: v.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	l, _ := e.store.Listing(v.ID)
	if l.Status != model.ListingPending {
		t.Fatalf("should stay pending until photo: %+v", l)
	}
	if err := e.svc.HandleProcessPhoto(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	l, _ = e.store.Listing(v.ID)
	if l.Status != model.ListingActive || l.PhotoObjectKey == nil {
		t.Fatalf("expected active with photo: %+v", l)
	}
	if e.objects.Has(model.UploadObjectKey(slot.UploadID)) {
		t.Fatal("original should be deleted")
	}
	if bytes.Contains(e.objects.GetBytes(*l.PhotoObjectKey), []byte("Exif")) {
		t.Fatal("processed still has Exif")
	}
}

func TestCloseAndAutoClose(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true)
	v := createListingPending(t, e, author)
	activate(t, e, v.ID)
	if err := e.svc.CloseListing(ctx, author, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CloseListing(ctx, author, v.ID); err != nil {
		t.Fatal(err)
	}
	l, _ := e.store.Listing(v.ID)
	if l.Status != model.ListingClosed {
		t.Fatalf("status %s", l.Status)
	}

	v2 := createListingPending(t, e, author)
	activate(t, e, v2.ID)
	err := e.store.InTx(ctx, func(tx port.Tx) error {
		return e.svc.HandleAutoClose(ctx, tx, v2.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	l2, _ := e.store.Listing(v2.ID)
	if l2.Status != model.ListingClosed {
		t.Fatalf("auto-close status %s", l2.Status)
	}
}
