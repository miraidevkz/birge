package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
)

// NATSFanout publishes and receives room notices on NATS core subjects
// birge.chat.room.<id>, not JetStream: notices are signals, PostgreSQL is the
// source of truth, and a gateway that misses one fills the gap by sequence.
// Notices carry IDs only.
type NATSFanout struct {
	Conn   *nats.Conn
	Logger *slog.Logger
}

var (
	_ port.Fanout = (*NATSFanout)(nil)
	_ port.Bus    = (*NATSFanout)(nil)
)

// Publish sends n to every gateway subscribed to the room.
func (f *NATSFanout) Publish(_ context.Context, n model.Notice) error {
	b, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal notice: %w", err)
	}
	if err := f.Conn.Publish(model.RoomSubject(n.RoomID), b); err != nil {
		return fmt.Errorf("nats publish: %w", err)
	}
	return nil
}

// Subscribe calls fn for the room's notices until unsubscribe is called.
// Malformed notices are dropped.
func (f *NATSFanout) Subscribe(roomID uuid.UUID, fn func(model.Notice)) (func(), error) {
	sub, err := f.Conn.Subscribe(model.RoomSubject(roomID), func(m *nats.Msg) {
		var n model.Notice
		if err := json.Unmarshal(m.Data, &n); err != nil || n.RoomID != roomID {
			f.log().Warn("chat notice dropped", "event", "ChatNoticeMalformed", "room_id", roomID)
			return
		}
		fn(n)
	})
	if err != nil {
		return nil, fmt.Errorf("nats subscribe: %w", err)
	}
	return func() { _ = sub.Unsubscribe() }, nil
}

func (f *NATSFanout) log() *slog.Logger {
	if f.Logger != nil {
		return f.Logger
	}
	return slog.Default()
}
