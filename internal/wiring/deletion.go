package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"

	assistantadapter "birge/internal/domains/assistant/adapter"
	assistantservice "birge/internal/domains/assistant/service"
	chatadapter "birge/internal/domains/chat/adapter"
	chatservice "birge/internal/domains/chat/service"
	circleadapter "birge/internal/domains/circle/adapter"
	circleservice "birge/internal/domains/circle/service"
	directoryadapter "birge/internal/domains/directory/adapter"
	directoryservice "birge/internal/domains/directory/service"
	emergencyadapter "birge/internal/domains/emergency/adapter"
	emergencyservice "birge/internal/domains/emergency/service"
	membershipadapter "birge/internal/domains/membership/adapter"
	membershipport "birge/internal/domains/membership/port"
	membershipservice "birge/internal/domains/membership/service"
	notifyadapter "birge/internal/domains/notify/adapter"
	notifyservice "birge/internal/domains/notify/service"
	privacymodel "birge/internal/domains/privacy/model"
	safetyadapter "birge/internal/domains/safety/adapter"
	safetyservice "birge/internal/domains/safety/service"
	sharingadapter "birge/internal/domains/sharing/adapter"
	sharingport "birge/internal/domains/sharing/port"
	sharingservice "birge/internal/domains/sharing/service"
	wellbeingadapter "birge/internal/domains/wellbeing/adapter"
	wellbeingservice "birge/internal/domains/wellbeing/service"
	"birge/internal/platform/clock"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// Deleters holds the account-deletion service of every bounded context that
// stores member data. Privacy is not here: it deletes its consents in its
// own privacy.DeletionDue timer, after the shred. Moderation and knowledge
// store only IDs, codes and staff IDs (see their READMEs).
type Deleters struct {
	Membership *membershipservice.Deletion
	Notify     *notifyservice.Deletion
	Safety     *safetyservice.Deletion
	Wellbeing  *wellbeingservice.Deletion
	Emergency  *emergencyservice.Deletion
	Assistant  *assistantservice.Deletion
	Circle     *circleservice.Deletion
	Chat       *chatservice.Deletion
	Sharing    *sharingservice.Deletion
	Directory  *directoryservice.Deletion
}

// NewDeleters wires the deletion services on pool. objects deletes sharing
// photos; identities deletes the Kratos identity (membership timer).
func NewDeleters(pool *pgxpool.Pool, objects sharingport.ObjectStore, identities membershipport.IdentityDeleter, clk clock.Clock, logger *slog.Logger) *Deleters {
	return &Deleters{
		Membership: &membershipservice.Deletion{Store: &membershipadapter.Postgres{Pool: pool}, Identities: identities, Clock: clk, Logger: logger},
		Notify:     &notifyservice.Deletion{Store: &notifyadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
		Safety:     &safetyservice.Deletion{Store: &safetyadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
		Wellbeing:  &wellbeingservice.Deletion{Store: &wellbeingadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
		Emergency:  &emergencyservice.Deletion{Store: &emergencyadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
		Assistant:  &assistantservice.Deletion{Store: &assistantadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
		Circle:     &circleservice.Deletion{Store: &circleadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
		Chat:       &chatservice.Deletion{Store: &chatadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
		Sharing:    &sharingservice.Deletion{Store: &sharingadapter.PostgresStore{Pool: pool}, Objects: objects, Clock: clk, Logger: logger},
		Directory:  &directoryservice.Deletion{Store: &directoryadapter.PostgresStore{Pool: pool}, Clock: clk, Logger: logger},
	}
}

// TimerHandlers returns the account-deletion timers: membership.DeleteIdentity
// (Kratos identity) and sharing.DeleteObjects (photo objects).
func (d *Deleters) TimerHandlers() map[string]timers.Handler {
	out := (&membershipadapter.TimerHandlers{Deletion: d.Membership}).Handlers()
	maps.Copy(out, (&sharingadapter.DeletionTimerHandlers{Deletion: d.Sharing}).Handlers())
	return out
}

// Consumers returns one DeletionConsumer per context.
func (d *Deleters) Consumers(js jetstream.JetStream, pool *pgxpool.Pool, logger *slog.Logger) []*DeletionConsumer {
	byContext := []struct {
		name    string
		deleter MemberDeleter
	}{
		{"membership", d.Membership}, {"notify", d.Notify}, {"safety", d.Safety},
		{"wellbeing", d.Wellbeing}, {"emergency", d.Emergency}, {"assistant", d.Assistant},
		{"circle", d.Circle}, {"chat", d.Chat}, {"sharing", d.Sharing}, {"directory", d.Directory},
	}
	out := make([]*DeletionConsumer, 0, len(byContext))
	for _, c := range byContext {
		out = append(out, &DeletionConsumer{JS: js, Pool: pool, Context: c.name, Deleter: c.deleter, Logger: logger})
	}
	return out
}

// MemberDeleter is the account-deletion entry point of one bounded context:
// service.Deletion.DeleteMember. It must be idempotent and run in one
// transaction of that context.
type MemberDeleter interface {
	DeleteMember(ctx context.Context, memberID uuid.UUID) error
}

// DeletionConsumer is the consumer of privacy.DataDeletionRequested for one
// bounded context (Context, for example "chat"): JetStream durable
// "<context>-deletion", platform.processed_events consumer
// "<context>.Deletion". Every context has its own consumer, so a failing
// context retries alone and never holds back the others.
//
// Order: the event is skipped when already processed; otherwise DeleteMember
// runs (its own transaction in the context) and the event is marked
// processed afterwards. A crash between the two runs DeleteMember again on
// redelivery, which is safe because it is idempotent; marking first could
// lose the deletion. The handler needs only the database, so an outage of
// another service cannot exhaust the redeliveries (docs/solved-problems.md).
type DeletionConsumer struct {
	JS      jetstream.JetStream
	Pool    *pgxpool.Pool
	Context string
	Deleter MemberDeleter
	Logger  *slog.Logger
}

// Durable is the JetStream durable consumer name.
func (c *DeletionConsumer) Durable() string { return c.Context + "-deletion" }

// Processed is the platform.processed_events consumer name.
func (c *DeletionConsumer) Processed() string { return c.Context + ".Deletion" }

// EnsureConsumer creates the durable consumer if needed.
func (c *DeletionConsumer) EnsureConsumer(ctx context.Context) (jetstream.Consumer, error) {
	cons, err := c.JS.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:       c.Durable(),
		FilterSubject: outbox.Subject(privacymodel.EventDataDeletionRequested),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       60 * time.Second,
		// Deletion must not give up: the privacy timer shreds the key at
		// due_at regardless, and the data has to be gone by then.
		MaxDeliver: -1,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s consumer: %w", c.Durable(), err)
	}
	return cons, nil
}

// HandleMessage processes one message body. It is safe to call with duplicates.
func (c *DeletionConsumer) HandleMessage(ctx context.Context, body []byte) error {
	var env outbox.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("unmarshal envelope: %w", err)
	}
	if env.Type != privacymodel.EventDataDeletionRequested {
		c.log().Info("ignoring event", "event", env.Type, "event_id", env.ID, "consumer", c.Processed())
		return nil
	}
	var data struct {
		MemberID uuid.UUID `json:"member_id"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return fmt.Errorf("parse %s: %w", env.Type, err)
	}
	if data.MemberID == uuid.Nil {
		return errors.New("data deletion requested: missing member_id")
	}
	return c.Handle(ctx, env.ID, data.MemberID)
}

// Handle runs the deletion for memberID unless eventID was processed already.
func (c *DeletionConsumer) Handle(ctx context.Context, eventID, memberID uuid.UUID) error {
	q := platformstore.New(c.Pool)
	done, err := q.EventProcessed(ctx, platformstore.EventProcessedParams{Consumer: c.Processed(), EventID: eventID})
	if err != nil {
		return fmt.Errorf("%s: processed lookup: %w", c.Processed(), err)
	}
	if done {
		c.log().Info("deletion already processed", "event", privacymodel.EventDataDeletionRequested,
			"event_id", eventID, "consumer", c.Processed())
		return nil
	}
	if err := c.Deleter.DeleteMember(ctx, memberID); err != nil {
		return fmt.Errorf("%s: %w", c.Processed(), err)
	}
	if _, err := q.MarkEventProcessed(ctx, platformstore.MarkEventProcessedParams{Consumer: c.Processed(), EventID: eventID}); err != nil {
		return fmt.Errorf("%s: mark processed: %w", c.Processed(), err)
	}
	return nil
}

// Run consumes until ctx is cancelled.
func (c *DeletionConsumer) Run(ctx context.Context) error {
	cons, err := c.EnsureConsumer(ctx)
	if err != nil {
		return err
	}
	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := c.HandleMessage(ctx, msg.Data()); err != nil {
			c.log().Error("member deletion", "event", "MemberDeletionFailed", "consumer", c.Processed(), "error", err)
			_ = msg.NakWithDelay(10 * time.Second)
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("consume %s: %w", c.Durable(), err)
	}
	defer cc.Stop()
	<-ctx.Done()
	return nil
}

func (c *DeletionConsumer) log() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}
