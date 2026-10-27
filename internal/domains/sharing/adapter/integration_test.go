package adapter_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/sharing/adapter"
	"birge/internal/domains/sharing/imageproc"
	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/service"
	"birge/internal/domains/sharing/sharingtest"
	"birge/internal/platform/auth"
	"birge/internal/platform/testinfra"
)

type env struct {
	pool    *pgxpool.Pool
	svc     *service.Service
	members *sharingtest.FakeMembers
	objects *sharingtest.MemoryObjects
	clock   *sharingtest.Clock
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	members := sharingtest.NewFakeMembers()
	objects := sharingtest.NewMemoryObjects()
	clk := &sharingtest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	return &env{pool: pool, members: members, objects: objects, clock: clk, svc: &service.Service{
		Store: &adapter.PostgresStore{Pool: pool}, Members: members, Blocks: members,
		Objects: objects, Images: imageproc.Processor{}, Clock: clk,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

func (e *env) member(verified bool) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{Verified: verified})
	return p
}

func jpegWithEXIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	raw := buf.Bytes()
	app1 := []byte{0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0, 9, 9, 9, 9, 9, 9, 9, 9}
	out := append([]byte{0xFF, 0xD8}, app1...)
	return append(out, raw[2:]...)
}

func TestListingFeedBlocksPhotoAndContactPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := e.member(true)
	viewer := e.member(true)
	blocked := e.member(true)
	e.members.Block(viewer.MemberID, blocked.MemberID)

	slot, err := e.svc.CreatePhotoUpload(ctx, author, model.ContentJPEG, 200)
	if err != nil {
		t.Fatal(err)
	}
	raw := jpegWithEXIF(t)
	if err := e.objects.Put(ctx, model.UploadObjectKey(slot.UploadID), raw, model.ContentJPEG); err != nil {
		t.Fatal(err)
	}
	listing, err := e.svc.CreateListing(ctx, author, uuid.Must(uuid.NewV7()), model.NewListing{
		Type: model.TypeGive, Category: "clothes", Title: "Example coat",
		Description: "Example description", DistrictID: uuid.New(),
		PhotoUploadID: &slot.UploadID, AcceptRules: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindListing, ObjectID: listing.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Still pending until photo processed.
	var status string
	if err := e.pool.QueryRow(ctx, `SELECT status FROM sharing.listings WHERE id=$1`, listing.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != model.ListingPending {
		t.Fatalf("status %s", status)
	}
	if err := e.svc.HandleProcessPhoto(ctx, listing.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `SELECT status FROM sharing.listings WHERE id=$1`, listing.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != model.ListingActive {
		t.Fatalf("status %s", status)
	}
	var photoKey *string
	if err := e.pool.QueryRow(ctx, `SELECT photo_object_key FROM sharing.listings WHERE id=$1`, listing.ID).Scan(&photoKey); err != nil {
		t.Fatal(err)
	}
	if photoKey == nil || bytes.Contains(e.objects.GetBytes(*photoKey), []byte("Exif")) {
		t.Fatal("processed photo missing or still has Exif")
	}

	hid, err := e.svc.CreateListing(ctx, blocked, uuid.Must(uuid.NewV7()), model.NewListing{
		Type: model.TypeNeed, Category: "toys", Title: "Example toy",
		Description: "Example description", DistrictID: uuid.New(), AcceptRules: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindListing, ObjectID: hid.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}

	page, err := e.svc.ListListings(ctx, viewer, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range page.Items {
		if it.ID == hid.ID {
			t.Fatal("blocked listing visible")
		}
		if it.AuthorID == viewer.MemberID {
			t.Fatal("own listing visible")
		}
	}
	found := false
	for _, it := range page.Items {
		if it.ID == listing.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("expected listing in feed")
	}

	if err := e.svc.RequestContact(ctx, viewer, listing.ID); err != nil {
		t.Fatal(err)
	}
	var contactID uuid.UUID
	if err := e.pool.QueryRow(ctx,
		`SELECT id FROM sharing.contact_requests WHERE listing_id=$1 AND requester_id=$2`,
		listing.ID, viewer.MemberID,
	).Scan(&contactID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.AcceptContact(ctx, author, contactID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM platform.outbox WHERE event_type='sharing.ContactAccepted'`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("ContactAccepted count %d", n)
	}
}
