// Package port defines the interfaces the notify service needs.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
)

// Recipients resolves DeliveryRequested recipient forms to delivery targets.
// Implemented later by emergency/safety; return model.ErrRecipientUnresolved when unknown.
// External trusted contacts (phone only) come back as Kind=external with Ref=trusted_contact_id.
type Recipients interface {
	Resolve(ctx context.Context, r model.Recipient) ([]model.ResolvedRecipient, error)
}

// PushMessage is a content-free push: the client renders Template with Params.
type PushMessage struct {
	MemberID       uuid.UUID
	DeviceID       uuid.UUID
	PushToken      string
	Platform       string
	NotificationID uuid.UUID
	Template       string
	Params         map[string]string
	Class          string
}

// PushSender delivers a push. Implementations must not log the token.
type PushSender interface {
	Send(ctx context.Context, msg PushMessage) (providerMessageID string, err error)
}

// SMSMessage carries only a neutral fixed text from config, never message content.
type SMSMessage struct {
	NotificationID uuid.UUID
	PhoneE164      string
	Text           string
}

// SMSSender delivers an SMS. Implementations must not log the phone number or text.
type SMSSender interface {
	Send(ctx context.Context, msg SMSMessage) (providerMessageID string, err error)
}

// Store is the persistence port for notify tables and the platform hooks it needs
// (processed_events dedup and delivery timers).
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	// MarkProcessed records eventID for the consumer. newly is false when already seen.
	MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (newly bool, err error)

	InsertNotification(ctx context.Context, n model.Notification) (created model.Notification, inserted bool, err error)
	GetNotification(ctx context.Context, id uuid.UUID) (model.Notification, error)
	GetNotificationByRequestRecipient(ctx context.Context, requestID uuid.UUID, kind string, ref uuid.UUID) (model.Notification, error)
	ListInbox(ctx context.Context, memberID uuid.UUID, cursorCreatedAt *time.Time, cursorID *uuid.UUID, limit int32) ([]model.Notification, error)
	MarkRead(ctx context.Context, id, memberID uuid.UUID, at time.Time) (model.Notification, error)

	InsertDelivery(ctx context.Context, notificationID uuid.UUID, channel, status string, at time.Time) (id uuid.UUID, inserted bool, err error)
	GetDelivery(ctx context.Context, notificationID uuid.UUID, channel string) (deliveryID uuid.UUID, status string, attempts int32, err error)
	UpdateDelivery(ctx context.Context, deliveryID uuid.UUID, status string, attempts int32, providerMessageID *string, at time.Time) error

	UpsertDevice(ctx context.Context, d model.Device) error
	DeleteDevice(ctx context.Context, memberID, deviceID uuid.UUID) error
	ListDevices(ctx context.Context, memberID uuid.UUID) ([]model.Device, error)

	SchedulePush(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error
	ScheduleExternalSMS(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error
	ScheduleCriticalSMS(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error
	CancelCriticalSMS(ctx context.Context, notificationID uuid.UUID, at time.Time) (cancelled int64, err error)
}
