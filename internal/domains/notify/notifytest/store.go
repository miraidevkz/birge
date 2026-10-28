// Package notifytest holds in-memory fakes for notify unit tests.
package notifytest

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/port"
)

// MemoryStore is an in-memory port.Store for unit tests.
type MemoryStore struct {
	mu sync.Mutex

	processed     map[string]map[uuid.UUID]struct{}
	notifications map[uuid.UUID]model.Notification
	byRequest     map[string]uuid.UUID // requestID|kind|ref -> notification id
	deliveries    map[string]memoryDelivery
	devices       map[string]model.Device
	timers        map[string]time.Time // kind|subject -> due
}

type memoryDelivery struct {
	ID                uuid.UUID
	NotificationID    uuid.UUID
	Channel           string
	Status            string
	Attempts          int32
	ProviderMessageID *string
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		processed:     make(map[string]map[uuid.UUID]struct{}),
		notifications: make(map[uuid.UUID]model.Notification),
		byRequest:     make(map[string]uuid.UUID),
		deliveries:    make(map[string]memoryDelivery),
		devices:       make(map[string]model.Device),
		timers:        make(map[string]time.Time),
	}
}

// InTx runs fn under a mutex (single-threaded "transaction").
func (s *MemoryStore) InTx(_ context.Context, fn func(tx port.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&memoryTx{s: s})
}

func timerKey(kind string, subject uuid.UUID) string {
	return kind + "|" + subject.String()
}

// DueTimers returns subject IDs for kind whose timer is due at or before now.
func (s *MemoryStore) DueTimers(kind string, now time.Time) []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uuid.UUID
	prefix := kind + "|"
	for k, due := range s.timers {
		if len(k) < len(prefix) || k[:len(prefix)] != prefix {
			continue
		}
		if !due.After(now) {
			id, err := uuid.Parse(k[len(prefix):])
			if err != nil {
				continue
			}
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// FireTimer removes the timer as if the worker completed it.
func (s *MemoryStore) FireTimer(kind string, id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.timers, timerKey(kind, id))
}

// DueCriticalSMS is a convenience wrapper around DueTimers for CriticalSMS.
func (s *MemoryStore) DueCriticalSMS(now time.Time) []uuid.UUID {
	return s.DueTimers(model.TimerCriticalSMS, now)
}

// FireCriticalSMS removes a CriticalSMS timer.
func (s *MemoryStore) FireCriticalSMS(id uuid.UUID) {
	s.FireTimer(model.TimerCriticalSMS, id)
}

type memoryTx struct {
	s *MemoryStore
}

// MarkProcessed records eventID for consumer; newly is false when already seen.
func (t *memoryTx) MarkProcessed(_ context.Context, consumer string, eventID uuid.UUID) (bool, error) {
	m := t.s.processed[consumer]
	if m == nil {
		m = make(map[uuid.UUID]struct{})
		t.s.processed[consumer] = m
	}
	if _, ok := m[eventID]; ok {
		return false, nil
	}
	m[eventID] = struct{}{}
	return true, nil
}

func reqKey(requestID uuid.UUID, kind string, ref uuid.UUID) string {
	return requestID.String() + "|" + kind + "|" + ref.String()
}

// InsertNotification inserts n; inserted is false when the request/recipient row already exists.
func (t *memoryTx) InsertNotification(_ context.Context, n model.Notification) (model.Notification, bool, error) {
	key := reqKey(n.RequestID, n.RecipientKind, n.RecipientRef)
	if id, ok := t.s.byRequest[key]; ok {
		return t.s.notifications[id], false, nil
	}
	if n.ID == uuid.Nil {
		n.ID = uuid.Must(uuid.NewV7())
	}
	if n.Params == nil {
		n.Params = map[string]string{}
	}
	t.s.notifications[n.ID] = n
	t.s.byRequest[key] = n.ID
	return n, true, nil
}

// GetNotification returns the notification or model.ErrNotFound.
func (t *memoryTx) GetNotification(_ context.Context, id uuid.UUID) (model.Notification, error) {
	n, ok := t.s.notifications[id]
	if !ok {
		return model.Notification{}, model.ErrNotFound
	}
	return n, nil
}

// GetNotificationByRequestRecipient returns the row for requestID/kind/ref or model.ErrNotFound.
func (t *memoryTx) GetNotificationByRequestRecipient(_ context.Context, requestID uuid.UUID, kind string, ref uuid.UUID) (model.Notification, error) {
	id, ok := t.s.byRequest[reqKey(requestID, kind, ref)]
	if !ok {
		return model.Notification{}, model.ErrNotFound
	}
	return t.s.notifications[id], nil
}

// ListInbox returns the member's inbox page (newest first), optionally after the cursor.
func (t *memoryTx) ListInbox(_ context.Context, memberID uuid.UUID, cursorCreatedAt *time.Time, cursorID *uuid.UUID, limit int32) ([]model.Notification, error) {
	var all []model.Notification
	for _, n := range t.s.notifications {
		if n.RecipientKind != model.RecipientKindMember || n.RecipientRef != memberID {
			continue
		}
		if cursorCreatedAt != nil && cursorID != nil {
			before := n.CreatedAt.Before(*cursorCreatedAt) ||
				(n.CreatedAt.Equal(*cursorCreatedAt) && bytesLess(n.ID, *cursorID))
			if !before {
				continue
			}
		}
		all = append(all, n)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return bytesLess(all[j].ID, all[i].ID)
	})
	if int(limit) < len(all) {
		all = all[:limit]
	}
	return all, nil
}

func bytesLess(a, b uuid.UUID) bool {
	ab, bb := a[:], b[:]
	for i := range ab {
		if ab[i] < bb[i] {
			return true
		}
		if ab[i] > bb[i] {
			return false
		}
	}
	return false
}

// MarkRead sets read_at (and acknowledged_at for critical) for the member's notification.
func (t *memoryTx) MarkRead(_ context.Context, id, memberID uuid.UUID, at time.Time) (model.Notification, error) {
	n, ok := t.s.notifications[id]
	if !ok || n.RecipientKind != model.RecipientKindMember || n.RecipientRef != memberID {
		return model.Notification{}, model.ErrNotFound
	}
	if n.ReadAt == nil {
		n.ReadAt = &at
	}
	if n.Class == model.ClassCritical && n.AcknowledgedAt == nil {
		n.AcknowledgedAt = &at
	}
	t.s.notifications[id] = n
	return n, nil
}

func deliveryKey(notificationID uuid.UUID, channel string) string {
	return notificationID.String() + "|" + channel
}

// InsertDelivery creates a delivery row; inserted is false when the channel row already exists.
func (t *memoryTx) InsertDelivery(_ context.Context, notificationID uuid.UUID, channel, status string, _ time.Time) (uuid.UUID, bool, error) {
	key := deliveryKey(notificationID, channel)
	if d, ok := t.s.deliveries[key]; ok {
		return d.ID, false, nil
	}
	id := uuid.Must(uuid.NewV7())
	t.s.deliveries[key] = memoryDelivery{
		ID:             id,
		NotificationID: notificationID,
		Channel:        channel,
		Status:         status,
	}
	return id, true, nil
}

// GetDelivery returns the delivery for notificationID/channel or model.ErrNotFound.
func (t *memoryTx) GetDelivery(_ context.Context, notificationID uuid.UUID, channel string) (uuid.UUID, string, int32, error) {
	d, ok := t.s.deliveries[deliveryKey(notificationID, channel)]
	if !ok {
		return uuid.Nil, "", 0, model.ErrNotFound
	}
	return d.ID, d.Status, d.Attempts, nil
}

// UpdateDelivery writes status, attempts and optional provider id for deliveryID.
func (t *memoryTx) UpdateDelivery(_ context.Context, deliveryID uuid.UUID, status string, attempts int32, providerMessageID *string, _ time.Time) error {
	for k, d := range t.s.deliveries {
		if d.ID == deliveryID {
			d.Status = status
			d.Attempts = attempts
			d.ProviderMessageID = providerMessageID
			t.s.deliveries[k] = d
			return nil
		}
	}
	return model.ErrNotFound
}

// UpsertDevice inserts or replaces the member's push device registration.
func (t *memoryTx) UpsertDevice(_ context.Context, d model.Device) error {
	t.s.devices[d.MemberID.String()+"|"+d.DeviceID.String()] = d
	return nil
}

// DeleteDevice removes the member's device registration if present.
func (t *memoryTx) DeleteDevice(_ context.Context, memberID, deviceID uuid.UUID) error {
	delete(t.s.devices, memberID.String()+"|"+deviceID.String())
	return nil
}

// DeleteRejectedDevice removes the registration while it still holds pushToken.
func (t *memoryTx) DeleteRejectedDevice(_ context.Context, memberID, deviceID uuid.UUID, pushToken string) (bool, error) {
	key := memberID.String() + "|" + deviceID.String()
	d, ok := t.s.devices[key]
	if !ok || d.PushToken != pushToken {
		return false, nil
	}
	delete(t.s.devices, key)
	return true, nil
}

// ListDevices returns every push device registered for the member.
func (t *memoryTx) ListDevices(_ context.Context, memberID uuid.UUID) ([]model.Device, error) {
	var out []model.Device
	for _, d := range t.s.devices {
		if d.MemberID == memberID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (t *memoryTx) schedule(kind string, notificationID uuid.UUID, dueAt time.Time) error {
	key := timerKey(kind, notificationID)
	if _, ok := t.s.timers[key]; ok {
		return nil
	}
	t.s.timers[key] = dueAt
	return nil
}

// SchedulePush records a notify.Push timer (dedup key = notification id).
func (t *memoryTx) SchedulePush(_ context.Context, notificationID uuid.UUID, dueAt time.Time) error {
	return t.schedule(model.TimerPush, notificationID, dueAt)
}

// ScheduleExternalSMS records a notify.ExternalSMS timer (dedup key = notification id).
func (t *memoryTx) ScheduleExternalSMS(_ context.Context, notificationID uuid.UUID, dueAt time.Time) error {
	return t.schedule(model.TimerExternalSMS, notificationID, dueAt)
}

// ScheduleCriticalSMS records a notify.CriticalSMS timer (dedup key = notification id).
func (t *memoryTx) ScheduleCriticalSMS(_ context.Context, notificationID uuid.UUID, dueAt time.Time) error {
	return t.schedule(model.TimerCriticalSMS, notificationID, dueAt)
}

// CancelCriticalSMS removes a pending CriticalSMS timer; returns how many were cancelled.
func (t *memoryTx) CancelCriticalSMS(_ context.Context, notificationID uuid.UUID, _ time.Time) (int64, error) {
	key := timerKey(model.TimerCriticalSMS, notificationID)
	if _, ok := t.s.timers[key]; !ok {
		return 0, nil
	}
	delete(t.s.timers, key)
	return 1, nil
}

// NotificationCount is a test helper.
func (s *MemoryStore) NotificationCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.notifications)
}

// NotificationByRequest is a test helper.
func (s *MemoryStore) NotificationByRequest(requestID uuid.UUID, kind string, ref uuid.UUID) (model.Notification, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byRequest[reqKey(requestID, kind, ref)]
	if !ok {
		return model.Notification{}, false
	}
	return s.notifications[id], true
}

// DeliveryStatus is a test helper.
func (s *MemoryStore) DeliveryStatus(notificationID uuid.UUID, channel string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deliveries[deliveryKey(notificationID, channel)]
	if !ok {
		return "", false
	}
	return d.Status, true
}

// DeviceCount is a test helper: how many devices the member has registered.
func (s *MemoryStore) DeviceCount(memberID uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.devices {
		if d.MemberID == memberID {
			n++
		}
	}
	return n
}

var (
	_ port.Store = (*MemoryStore)(nil)
	_ port.Tx    = (*memoryTx)(nil)
)

var errNoV7 = errors.New("uuid v7 unavailable")

func init() {
	if _, err := uuid.NewV7(); err != nil {
		panic(errNoV7)
	}
}
