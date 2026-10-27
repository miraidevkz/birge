package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	chatadapter "birge/internal/domains/chat/adapter"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipmodel "birge/internal/domains/membership/model"
	membershipport "birge/internal/domains/membership/port"
	platformdb "birge/internal/platform/db"
)

// demoPresetChatRooms names the DEMO chat rooms in the DemoPreset log.
const demoPresetChatRooms = "chat_rooms"

// seedDemoChat creates one DEMO district room per district and one topic
// room per needs code of the (already seeded) reference data, when the chat
// rooms table is empty. Titles are locale keys. A no-op until the chat
// migration is applied at merge, and on every later migrate.
func seedDemoChat(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	pool, err := platformdb.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: demo chat rooms: %w", err)
	}
	defer pool.Close()
	var cities []membershipmodel.City
	var needs []string
	if err := (&membershipadapter.Postgres{Pool: pool}).InTx(ctx, func(tx membershipport.Tx) error {
		var err error
		if cities, err = tx.Cities(ctx); err != nil {
			return err
		}
		needs, err = tx.Needs(ctx)
		return err
	}); err != nil {
		return fmt.Errorf("migrate: demo chat rooms: %w", err)
	}
	var rooms []chatadapter.DemoRoom
	for _, c := range cities {
		for _, d := range c.Districts {
			id := d.ID
			rooms = append(rooms, chatadapter.DemoRoom{DistrictID: &id, TitleKey: "chat.room.district." + d.Code})
		}
	}
	for _, n := range needs {
		rooms = append(rooms, chatadapter.DemoRoom{TopicCode: n, TitleKey: "chat.room.topic." + n})
	}
	seeded, err := chatadapter.SeedDemoIfEmpty(ctx, pool, rooms, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("migrate: demo chat rooms: %w", err)
	}
	if seeded {
		LogDemoPresets(logger, []string{demoPresetChatRooms})
	}
	return nil
}
