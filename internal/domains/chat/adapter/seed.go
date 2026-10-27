package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/chat/model"
	chatstore "birge/internal/domains/chat/store"
	"birge/internal/platform/db"
)

// DemoRoom is one DEMO group room for a fresh deployment: a district room
// (DistrictID set) or a topic room (TopicCode set), titled by a locale key.
type DemoRoom struct {
	DistrictID *uuid.UUID
	TopicCode  string
	TitleKey   string
}

// SeedDemoIfEmpty inserts the DEMO group rooms when chat.rooms is empty. It
// never overwrites, and is a no-op while the chat schema is not applied yet
// (the migration is generated at merge). seeded is true only when this call
// inserted the rooms.
func SeedDemoIfEmpty(ctx context.Context, pool *pgxpool.Pool, rooms []DemoRoom, now time.Time) (seeded bool, err error) {
	if len(rooms) == 0 {
		return false, nil
	}
	err = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		q := chatstore.New(tx)
		present, err := q.ChatSchemaExists(ctx)
		if err != nil {
			return fmt.Errorf("chat: schema check: %w", err)
		}
		if !present {
			return nil
		}
		empty, err := q.ChatRoomsEmpty(ctx)
		if err != nil {
			return fmt.Errorf("chat: empty check: %w", err)
		}
		if !empty {
			return nil
		}
		t := Bind(tx)
		for _, r := range rooms {
			id, err := uuid.NewV7()
			if err != nil {
				return fmt.Errorf("chat: room id: %w", err)
			}
			room := model.Room{ID: id, TitleKey: r.TitleKey, CreatedAt: now}
			if r.DistrictID != nil {
				room.Kind, room.DistrictID = model.KindDistrict, r.DistrictID
			} else {
				room.Kind, room.TopicCode = model.KindTopic, r.TopicCode
			}
			if _, _, err := t.InsertGroupRoom(ctx, room); err != nil {
				return err
			}
		}
		seeded = true
		return nil
	})
	return seeded, err
}
