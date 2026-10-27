package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"

	emergencymodel "birge/internal/domains/emergency/model"
	emergencyservice "birge/internal/domains/emergency/service"
	membershipservice "birge/internal/domains/membership/service"
	"birge/internal/platform/clock"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
)

const (
	sosSharedConsumerName  = "emergency-sos-shared"
	sosSharedFilterSubject = "birge.events.emergency.SosShared"
	// sosSharedProcessed is the processed_events consumer name (ADR-0007).
	sosSharedProcessed = "emergency.SosShared"
)

// SosSharedConsumer turns emergency.SosShared into one notify.DeliveryRequested
// per verified member of the district (class normal, template
// emergency.sos_shared), excluding the author and anyone blocked with her.
// Deduplicated through platform.processed_events.
type SosSharedConsumer struct {
	JS        jetstream.JetStream
	Pool      *pgxpool.Pool
	Members   *membershipservice.Service
	Emergency *emergencyservice.Service
	Clock     clock.Clock
	Logger    *slog.Logger
}

// EnsureConsumer creates the durable consumer if needed.
func (c *SosSharedConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:       sosSharedConsumerName,
		FilterSubject: sosSharedFilterSubject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxDeliver:    10,
	})
	if err != nil {
		return nil, fmt.Errorf("create sos-shared consumer: %w", err)
	}
	return cons, nil
}

// HandleMessage processes one JetStream message. It is safe to call with duplicates.
func (c *SosSharedConsumer) HandleMessage(ctx context.Context, msg jetstream.Msg) error {
	var env outbox.Envelope
	if err := json.Unmarshal(msg.Data(), &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Type != emergencymodel.EventSosShared {
		c.log().Info("ignoring event", "event", env.Type, "event_id", env.ID)
		return nil
	}
	var data emergencymodel.SosSharedData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return fmt.Errorf("parse SosShared: %w", err)
	}
	if data.SosID == uuid.Nil || data.DistrictID == uuid.Nil {
		return fmt.Errorf("sos shared: missing sos_id or district_id")
	}
	return c.handle(ctx, env.ID, data)
}

func (c *SosSharedConsumer) handle(ctx context.Context, eventID uuid.UUID, data emergencymodel.SosSharedData) error {
	author, err := c.Emergency.SosAuthor(ctx, data.SosID)
	if err != nil {
		return fmt.Errorf("sos author: %w", err)
	}
	members, err := c.Members.ListVerifiedInDistrict(ctx, data.DistrictID)
	if err != nil {
		return fmt.Errorf("list verified: %w", err)
	}
	recipients := make([]uuid.UUID, 0, len(members))
	for _, id := range members {
		if id == author {
			continue
		}
		blocked, err := c.Members.IsBlocked(ctx, author, id)
		if err != nil {
			return fmt.Errorf("is blocked: %w", err)
		}
		if blocked {
			continue
		}
		recipients = append(recipients, id)
	}

	now := c.Clock.Now().UTC()
	return db.InTx(ctx, c.Pool, func(tx pgx.Tx) error {
		q := platformstore.New(tx)
		n, err := q.MarkEventProcessed(ctx, platformstore.MarkEventProcessedParams{
			Consumer: sosSharedProcessed, EventID: eventID,
		})
		if err != nil {
			return fmt.Errorf("mark processed: %w", err)
		}
		if n == 0 {
			c.log().Info("sos shared already processed", "event", emergencymodel.EventSosShared, "event_id", eventID)
			return nil
		}
		params := map[string]string{"sos_id": data.SosID.String()}
		for _, memberID := range recipients {
			ev := emergencymodel.Delivery(data.SosID, emergencymodel.MemberRecipient(memberID),
				emergencymodel.ClassNormal, emergencymodel.TemplateSosShared, params)
			if _, err := outbox.Enqueue(ctx, q, now, "", outbox.Event{
				Type: ev.Type, Version: ev.Version, Data: ev.Data,
			}); err != nil {
				return err
			}
		}
		c.log().Info("sos shared district push", "event", emergencymodel.EventSosShared,
			"sos_id", data.SosID, "recipients", len(recipients))
		return nil
	})
}

// Run consumes until ctx is cancelled.
func (c *SosSharedConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg); err != nil {
			c.log().Error("sos shared handler", "error", err)
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("consume sos shared: %w", err)
	}
	defer cc.Stop()
	<-ctx.Done()
	return nil
}

func (c *SosSharedConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
