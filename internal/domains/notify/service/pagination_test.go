package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/notifytest"
	"birge/internal/domains/notify/service"
)

func TestListInboxNewestFirstWithCursor(t *testing.T) {
	t.Parallel()
	member := uuid.Must(uuid.NewV7())
	store := notifytest.NewMemoryStore()
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	clk := &fixedClock{t: base}
	svc := &service.Service{
		Store:      store,
		Recipients: &notifytest.FakeRecipients{},
		Push:       &notifytest.FakePush{},
		Clock:      clk,
		Logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	ctx := context.Background()
	var ids []uuid.UUID
	for i := 0; i < 5; i++ {
		clk.t = base.Add(time.Duration(i) * time.Minute)
		eventID := uuid.Must(uuid.NewV7())
		reqID := uuid.Must(uuid.NewV7())
		if err := svc.HandleDeliveryRequested(ctx, eventID, model.DeliveryRequest{
			RequestID: reqID,
			Recipient: model.Recipient{MemberID: &member},
			Class:     model.ClassNormal,
			Template:  "system.ping",
		}); err != nil {
			t.Fatal(err)
		}
		fireDue(t, svc, store, model.TimerPush, clk.t)
		n, ok := store.NotificationByRequest(reqID, model.RecipientKindMember, member)
		if !ok {
			t.Fatal("missing notification")
		}
		ids = append(ids, n.ID)
	}

	page1, err := svc.ListInbox(ctx, member, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page1 len = %d", len(page1.Items))
	}
	if page1.Items[0].ID != ids[4] || page1.Items[1].ID != ids[3] {
		t.Fatalf("page1 order = %v, %v; want newest first", page1.Items[0].ID, page1.Items[1].ID)
	}
	if page1.NextCursor == "" {
		t.Fatal("expected next cursor")
	}

	page2, err := svc.ListInbox(ctx, member, page1.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Items) != 2 {
		t.Fatalf("page2 len = %d", len(page2.Items))
	}
	if page2.Items[0].ID != ids[2] || page2.Items[1].ID != ids[1] {
		t.Fatalf("page2 order unexpected")
	}

	page3, err := svc.ListInbox(ctx, member, page2.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page3.Items) != 1 || page3.Items[0].ID != ids[0] {
		t.Fatalf("page3 = %+v", page3.Items)
	}
	if page3.NextCursor != "" {
		t.Fatalf("unexpected next cursor %q", page3.NextCursor)
	}
}

func TestListInboxInvalidCursor(t *testing.T) {
	t.Parallel()
	svc := &service.Service{
		Store:  notifytest.NewMemoryStore(),
		Clock:  fixedClock{t: time.Now().UTC()},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	_, err := svc.ListInbox(context.Background(), uuid.Must(uuid.NewV7()), "not-a-cursor", 10)
	if !errors.Is(err, model.ErrInvalidCursor) {
		t.Fatalf("err = %v, want ErrInvalidCursor", err)
	}
}
