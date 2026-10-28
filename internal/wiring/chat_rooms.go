package wiring

import (
	"context"
	"fmt"
	"log/slog"

	chatadapter "birge/internal/domains/chat/adapter"
	chatservice "birge/internal/domains/chat/service"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipmodel "birge/internal/domains/membership/model"
	membershipport "birge/internal/domains/membership/port"
	"birge/internal/platform/clock"
	platformdb "birge/internal/platform/db"
)

// Group room title keys: the clients resolve "chat.room.district.<code>" and
// "chat.room.topic.<code>" to a localized name from the reference labels
// (district.<code>, needs.<code>), so the key carries the code and nothing
// else.
const (
	districtRoomTitlePrefix = "chat.room.district."
	topicRoomTitlePrefix    = "chat.room.topic."
)

// ensureChatRooms gives every active membership district and needs code its
// chat group room, on every migrate, after syncReference. It passes the
// active reference data from membership to chat's EnsureGroupRooms, so chat
// never reads membership tables. Only adds: existing rooms (the DEMO rooms
// of an older stand included) keep their ids, and the room of a deactivated
// district or code stays with its members and history; it is just not
// listed as joinable for anyone who did not keep that district.
func ensureChatRooms(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	pool, err := platformdb.Open(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: chat rooms: %w", err)
	}
	defer pool.Close()
	var cities []membershipmodel.City
	var needs []string
	// Cities lists only active cities with their active districts, and Needs
	// only active codes.
	if err := (&membershipadapter.Postgres{Pool: pool}).InTx(ctx, func(tx membershipport.Tx) error {
		var err error
		if cities, err = tx.Cities(ctx); err != nil {
			return err
		}
		needs, err = tx.Needs(ctx)
		return err
	}); err != nil {
		return fmt.Errorf("migrate: chat rooms: %w", err)
	}
	var districts []chatservice.DistrictRoomRef
	for _, c := range cities {
		for _, d := range c.Districts {
			districts = append(districts, chatservice.DistrictRoomRef{DistrictID: d.ID, TitleKey: districtRoomTitlePrefix + d.Code})
		}
	}
	topics := make([]chatservice.TopicRoomRef, 0, len(needs))
	for _, n := range needs {
		topics = append(topics, chatservice.TopicRoomRef{Code: n, TitleKey: topicRoomTitlePrefix + n})
	}
	// Room creation uses only the store and the clock.
	svc := &chatservice.Service{Store: &chatadapter.PostgresStore{Pool: pool}, Clock: clock.System{}, Logger: logger}
	res, err := svc.EnsureGroupRooms(ctx, districts, topics)
	if err != nil {
		return fmt.Errorf("migrate: chat rooms: %w", err)
	}
	logger.Info("chat rooms ensured", "event", "ChatRoomsEnsured",
		"districts", len(districts), "districts_created", res.DistrictsCreated,
		"topics", len(topics), "topics_created", res.TopicsCreated)
	return nil
}
