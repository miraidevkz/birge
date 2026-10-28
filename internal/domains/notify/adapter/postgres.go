// Package adapter implements notify ports on PostgreSQL and platform hooks
// (processed_events dedup and delivery timers).
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/port"
	notifystore "birge/internal/domains/notify/store"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL (notify schema + platform hooks).
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction.
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(&postgresTx{
			notify:   notifystore.New(pgxTx),
			platform: platformstore.New(pgxTx),
		})
	})
}

type postgresTx struct {
	notify   *notifystore.Queries
	platform *platformstore.Queries
}

// MarkProcessed records eventID for consumer; newly is false when already seen.
func (t *postgresTx) MarkProcessed(ctx context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	n, err := t.platform.MarkEventProcessed(ctx, platformstore.MarkEventProcessedParams{
		Consumer: consumer,
		EventID:  eventID,
	})
	if err != nil {
		return false, fmt.Errorf("mark processed: %w", err)
	}
	return n == 1, nil
}

// InsertNotification inserts n; inserted is false when the request/recipient row already exists.
func (t *postgresTx) InsertNotification(ctx context.Context, n model.Notification) (model.Notification, bool, error) {
	params, err := marshalParams(n.Params)
	if err != nil {
		return model.Notification{}, false, err
	}
	row, err := t.notify.InsertNotification(ctx, notifystore.InsertNotificationParams{
		RequestID:     n.RequestID,
		RecipientKind: n.RecipientKind,
		RecipientRef:  n.RecipientRef,
		Template:      n.Template,
		Params:        params,
		Class:         n.Class,
		CreatedAt:     n.CreatedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Notification{}, false, nil
	}
	if err != nil {
		return model.Notification{}, false, fmt.Errorf("insert notification: %w", err)
	}
	out, err := mapNotification(row)
	return out, true, err
}

// GetNotification returns the notification or model.ErrNotFound.
func (t *postgresTx) GetNotification(ctx context.Context, id uuid.UUID) (model.Notification, error) {
	row, err := t.notify.GetNotification(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Notification{}, model.ErrNotFound
	}
	if err != nil {
		return model.Notification{}, err
	}
	return mapNotification(row)
}

// GetNotificationByRequestRecipient returns the row for requestID/kind/ref or model.ErrNotFound.
func (t *postgresTx) GetNotificationByRequestRecipient(ctx context.Context, requestID uuid.UUID, kind string, ref uuid.UUID) (model.Notification, error) {
	row, err := t.notify.GetNotificationByRequestRecipient(ctx, notifystore.GetNotificationByRequestRecipientParams{
		RequestID:     requestID,
		RecipientKind: kind,
		RecipientRef:  ref,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Notification{}, model.ErrNotFound
	}
	if err != nil {
		return model.Notification{}, err
	}
	return mapNotification(row)
}

// ListInbox returns the member's inbox page (newest first), optionally after the cursor.
func (t *postgresTx) ListInbox(ctx context.Context, memberID uuid.UUID, cursorCreatedAt *time.Time, cursorID *uuid.UUID, limit int32) ([]model.Notification, error) {
	rows, err := t.notify.ListInbox(ctx, notifystore.ListInboxParams{
		RecipientRef:    memberID,
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		PageLimit:       limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]model.Notification, 0, len(rows))
	for _, row := range rows {
		n, err := mapNotification(row)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// MarkRead sets read_at (and acknowledged_at for critical) for the member's
// notification; returns model.ErrNotFound when absent.
func (t *postgresTx) MarkRead(ctx context.Context, id, memberID uuid.UUID, at time.Time) (model.Notification, error) {
	row, err := t.notify.MarkNotificationRead(ctx, notifystore.MarkNotificationReadParams{
		ReadAt:       &at,
		ID:           id,
		RecipientRef: memberID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Notification{}, model.ErrNotFound
	}
	if err != nil {
		return model.Notification{}, err
	}
	return mapNotification(row)
}

// InsertDelivery creates a delivery row; inserted is false when the channel row already exists.
func (t *postgresTx) InsertDelivery(ctx context.Context, notificationID uuid.UUID, channel, status string, at time.Time) (uuid.UUID, bool, error) {
	row, err := t.notify.InsertDelivery(ctx, notifystore.InsertDeliveryParams{
		NotificationID: notificationID,
		Channel:        channel,
		Status:         status,
		Attempts:       0,
		CreatedAt:      at,
		UpdatedAt:      at,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("insert delivery: %w", err)
	}
	return row.ID, true, nil
}

// GetDelivery returns the delivery for notificationID/channel or model.ErrNotFound.
func (t *postgresTx) GetDelivery(ctx context.Context, notificationID uuid.UUID, channel string) (uuid.UUID, string, int32, error) {
	row, err := t.notify.GetDelivery(ctx, notifystore.GetDeliveryParams{
		NotificationID: notificationID,
		Channel:        channel,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", 0, model.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, "", 0, err
	}
	return row.ID, row.Status, row.Attempts, nil
}

// UpdateDelivery writes status, attempts and optional provider id for deliveryID.
func (t *postgresTx) UpdateDelivery(ctx context.Context, deliveryID uuid.UUID, status string, attempts int32, providerMessageID *string, at time.Time) error {
	return t.notify.UpdateDeliveryStatus(ctx, notifystore.UpdateDeliveryStatusParams{
		Status:            status,
		Attempts:          attempts,
		ProviderMessageID: providerMessageID,
		UpdatedAt:         at,
		ID:                deliveryID,
	})
}

// UpsertDevice inserts or replaces the member's push device registration.
func (t *postgresTx) UpsertDevice(ctx context.Context, d model.Device) error {
	return t.notify.UpsertDevice(ctx, notifystore.UpsertDeviceParams{
		MemberID:  d.MemberID,
		DeviceID:  d.DeviceID,
		Platform:  d.Platform,
		PushToken: d.PushToken,
		UpdatedAt: d.UpdatedAt,
	})
}

// DeleteDevice removes the member's device registration if present.
func (t *postgresTx) DeleteDevice(ctx context.Context, memberID, deviceID uuid.UUID) error {
	_, err := t.notify.DeleteDevice(ctx, notifystore.DeleteDeviceParams{
		MemberID: memberID,
		DeviceID: deviceID,
	})
	return err
}

// DeleteRejectedDevice removes the registration while it still holds pushToken.
func (t *postgresTx) DeleteRejectedDevice(ctx context.Context, memberID, deviceID uuid.UUID, pushToken string) (bool, error) {
	n, err := t.notify.DeleteRejectedDevice(ctx, notifystore.DeleteRejectedDeviceParams{
		MemberID:  memberID,
		DeviceID:  deviceID,
		PushToken: pushToken,
	})
	return n > 0, err
}

// ListDevices returns every push device registered for the member.
func (t *postgresTx) ListDevices(ctx context.Context, memberID uuid.UUID) ([]model.Device, error) {
	rows, err := t.notify.ListDevices(ctx, memberID)
	if err != nil {
		return nil, err
	}
	out := make([]model.Device, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.Device{
			MemberID:  r.MemberID,
			DeviceID:  r.DeviceID,
			Platform:  r.Platform,
			PushToken: r.PushToken,
			UpdatedAt: r.UpdatedAt,
		})
	}
	return out, nil
}

// SchedulePush enqueues notify.Push (dedup key = notification id) in this transaction.
func (t *postgresTx) SchedulePush(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error {
	_, err := timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerPush,
		SubjectID: notificationID,
		DedupKey:  notificationID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
	return err
}

// ScheduleExternalSMS enqueues notify.ExternalSMS (dedup key = notification id) in this transaction.
func (t *postgresTx) ScheduleExternalSMS(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error {
	_, err := timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerExternalSMS,
		SubjectID: notificationID,
		DedupKey:  notificationID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
	return err
}

// ScheduleCriticalSMS enqueues notify.CriticalSMS (dedup key = notification id) in this transaction.
func (t *postgresTx) ScheduleCriticalSMS(ctx context.Context, notificationID uuid.UUID, dueAt time.Time) error {
	_, err := timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerCriticalSMS,
		SubjectID: notificationID,
		DedupKey:  notificationID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
	return err
}

// CancelCriticalSMS cancels a pending CriticalSMS timer; returns how many rows were cancelled.
func (t *postgresTx) CancelCriticalSMS(ctx context.Context, notificationID uuid.UUID, at time.Time) (int64, error) {
	return timers.Cancel(ctx, t.platform, at, model.TimerCriticalSMS, notificationID)
}

func mapNotification(row notifystore.NotifyNotification) (model.Notification, error) {
	params, err := unmarshalParams(row.Params)
	if err != nil {
		return model.Notification{}, err
	}
	return model.Notification{
		ID:             row.ID,
		RequestID:      row.RequestID,
		RecipientKind:  row.RecipientKind,
		RecipientRef:   row.RecipientRef,
		Template:       row.Template,
		Params:         params,
		Class:          row.Class,
		CreatedAt:      row.CreatedAt,
		ReadAt:         row.ReadAt,
		AcknowledgedAt: row.AcknowledgedAt,
	}, nil
}

func marshalParams(m map[string]string) (json.RawMessage, error) {
	if m == nil {
		return json.RawMessage(`{}`), nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	return b, nil
}

func unmarshalParams(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 {
		return map[string]string{}, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("unmarshal params: %w", err)
	}
	if m == nil {
		m = map[string]string{}
	}
	return m, nil
}

var _ port.Store = (*PostgresStore)(nil)
var _ port.Tx = (*postgresTx)(nil)
