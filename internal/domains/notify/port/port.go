// Package port defines the interfaces the notify service needs.
package port

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
)

// Recipients resolves DeliveryRequested recipient forms to delivery targets
// and looks up phones at SMS send time. Implemented by wiring (membership +
// Kratos admin); emergency/safety forms return model.ErrRecipientUnresolved
// until those contexts are wired. External trusted contacts come back as
// Kind=external with Ref=trusted_contact_id.
type Recipients interface {
	// Resolve returns kind, ref and language only. It must not look up phones
	// or call the identity provider: a temporary identity outage must not
	// block consuming DeliveryRequested.
	Resolve(ctx context.Context, r model.Recipient) ([]model.ResolvedRecipient, error)
	// PhoneFor returns the E.164 phone for a stored recipient, or "" when the
	// identity has none. Callers invoke it outside any database transaction,
	// right before sending SMS. A transient failure must be returned as an
	// error so the timer retries; never treat it as "no phone".
	PhoneFor(ctx context.Context, kind string, ref uuid.UUID) (string, error)
}

// PushMessage is a content-free push. Providers put only NotificationID and
// Title on the wire (ADR-0013): the app opens the inbox, which is the source
// of truth, and renders Template with Params there. Template, Params and
// Class stay on this side so a provider adapter never needs them; they must
// not be copied into a provider payload, because even the template key tells
// Google or Apple what kind of event happened (for example an SOS).
type PushMessage struct {
	MemberID       uuid.UUID
	DeviceID       uuid.UUID
	PushToken      string
	Platform       string
	NotificationID uuid.UUID
	// Title is the neutral title in the member's language, chosen by the
	// service from configuration. It never names the event.
	Title    string
	Template string
	Params   map[string]string
	Class    string
}

// Provider error classes. Push and SMS adapters wrap one of these with %w so
// the service can decide without knowing the provider. Any other error is
// transient: the delivery is marked failed and the platform timer retries it
// with backoff (timers.Backoff). Error texts must never contain the phone
// number, the push token, the message text or a credential.
var (
	// ErrTokenInvalid means the push provider says the device token no longer
	// exists or belongs to another app. The service removes that device
	// registration and does not retry it; the app registers a fresh token on
	// its next start.
	ErrTokenInvalid = errors.New("notify: push token rejected by the provider")
	// ErrPermanent means the provider rejected this message for a reason a
	// retry cannot fix (malformed number, forbidden text, oversized payload).
	// The delivery is marked failed and not retried.
	ErrPermanent = errors.New("notify: message rejected permanently by the provider")
)

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

// SMSLinks makes the link an SMS text carries in place of
// model.SMSLinkPlaceholder (the emergency plan link and the SOS link of an
// external trusted contact). It is called at send time, right before the
// provider, outside any database transaction. The link may hold a one-time
// secret: callers must never log or store it.
type SMSLinks interface {
	// LinkFor returns the absolute URL for the notification's template,
	// recipient and params (IDs only, such as sos_id),
	// model.ErrNotFound when there is none, or another error for a transient
	// failure. What the service does then depends on the class: an ordinary
	// SMS is skipped (not found) or retried (transient); a critical one goes
	// out at once with the template's text without the link
	// (model.SMSNoLinkSuffix).
	LinkFor(ctx context.Context, template, recipientKind string, recipientRef uuid.UUID, params map[string]string) (string, error)
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

	// InsertNotification inserts n; inserted is false when the request/recipient row already exists.
	InsertNotification(ctx context.Context, n model.Notification) (created model.Notification, inserted bool, err error)
	// GetNotification returns the notification or model.ErrNotFound.
	GetNotification(ctx context.Context, id uuid.UUID) (model.Notification, error)
	// GetNotificationByRequestRecipient returns the row for requestID/kind/ref or model.ErrNotFound.
	GetNotificationByRequestRecipient(ctx context.Context, requestID uuid.UUID, kind string, ref uuid.UUID) (model.Notification, error)
	// ListInbox returns the member's inbox page (newest first), optionally after the cursor.
	ListInbox(ctx context.Context, memberID uuid.UUID, cursorCreatedAt *time.Time, cursorID *uuid.UUID, limit int32) ([]model.Notification, error)
	// MarkRead sets read_at (and acknowledged_at for critical); returns model.ErrNotFound when absent.
	MarkRead(ctx context.Context, id, memberID uuid.UUID, at time.Time) (model.Notification, error)

	// InsertDelivery creates a delivery row; inserted is false when the channel row already exists.
	InsertDelivery(ctx context.Context, notificationID uuid.UUID, channel, status string, at time.Time) (id uuid.UUID, inserted bool, err error)
	// GetDelivery returns the delivery for notificationID/channel or model.ErrNotFound.
	GetDelivery(ctx context.Context, notificationID uuid.UUID, channel string) (deliveryID uuid.UUID, status string, attempts int32, err error)
	// UpdateDelivery writes status, attempts and optional provider id for deliveryID.
	UpdateDelivery(ctx context.Context, deliveryID uuid.UUID, status string, attempts int32, providerMessageID *string, at time.Time) error

	// UpsertDevice inserts or replaces the member's push device registration.
	UpsertDevice(ctx context.Context, d model.Device) error
	// DeleteDevice removes the member's device registration if present.
	DeleteDevice(ctx context.Context, memberID, deviceID uuid.UUID) error
	// ListDevices returns every push device registered for the member.
	ListDevices(ctx context.Context, memberID uuid.UUID) ([]model.Device, error)
	// DeleteRejectedDevice removes the registration only while it still holds
	// pushToken (the token the provider rejected), so a token the app
	// re-registered meanwhile survives. removed is false when nothing matched.
	DeleteRejectedDevice(ctx context.Context, memberID, deviceID uuid.UUID, pushToken string) (removed bool, err error)

	// SchedulePush enqueues notify.Push (dedup key = notification id) in this transaction.
	SchedulePush(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error
	// ScheduleExternalSMS enqueues notify.ExternalSMS (dedup key = notification id) in this transaction.
	ScheduleExternalSMS(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error
	// ScheduleCriticalSMS enqueues notify.CriticalSMS (dedup key = notification id) in this transaction.
	ScheduleCriticalSMS(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error
	// CancelCriticalSMS cancels a pending CriticalSMS timer; returns how many rows were cancelled.
	CancelCriticalSMS(ctx context.Context, notificationID uuid.UUID, at time.Time) (cancelled int64, err error)
}
