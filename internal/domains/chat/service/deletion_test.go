package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/service"
	"birge/internal/platform/clock"
)

type fakeChatEraser struct {
	held []uuid.UUID
	err  error
}

func (f *fakeChatEraser) EraseMember(context.Context, uuid.UUID, time.Time) ([]uuid.UUID, error) {
	return f.held, f.err
}

// Held messages are logged by message ID only, never next to the member.
func TestDeletionLogsLegalHoldByRecordID(t *testing.T) {
	var buf bytes.Buffer
	held := uuid.New()
	member := uuid.New()
	d := &service.Deletion{
		Store: &fakeChatEraser{held: []uuid.UUID{held}}, Clock: clock.System{},
		Logger: slog.New(slog.NewJSONHandler(&buf, nil)),
	}
	if err := d.DeleteMember(context.Background(), member); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatal(err)
		}
		if rec["event"] != "DeletionSkippedLegalHold" {
			continue
		}
		found = true
		if rec["message_id"] != held.String() || strings.Contains(line, member.String()) {
			t.Fatalf("legal hold log line: %s", line)
		}
	}
	if !found {
		t.Fatalf("no DeletionSkippedLegalHold line: %s", buf.String())
	}

	d.Store = &fakeChatEraser{err: errors.New("database down")}
	if err := d.DeleteMember(context.Background(), member); err == nil {
		t.Fatal("store failure not returned (the consumer must retry)")
	}
}
