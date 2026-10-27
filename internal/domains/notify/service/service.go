// Package service implements notify use cases: inbox, devices, DeliveryRequested
// consumption and the critical SMS escalation ladder.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/port"
	"birge/internal/domains/notify/sms/guard"
	"birge/internal/platform/clock"
)

const (
	defaultInboxLimit       = 20
	maxInboxLimit           = 50
	defaultCriticalSMSAfter = 60 * time.Second
)

// Config holds notify runtime settings loaded from the environment by the wiring layer.
type Config struct {
	// CriticalSMSAfter is how long to wait for acknowledgement before SMS (default 60s).
	CriticalSMSAfter time.Duration
	// SMSTexts maps template key -> language -> neutral fixed SMS body.
	SMSTexts map[string]map[string]string
}

// Service is the notify application service.
type Service struct {
	Store      port.Store
	Recipients port.Recipients
	Push       port.PushSender
	SMS        port.SMSSender
	Guard      *guard.Guard
	Clock      clock.Clock
	Logger     *slog.Logger
	Config     Config
}

func (s *Service) criticalAfter() time.Duration {
	if s.Config.CriticalSMSAfter > 0 {
		return s.Config.CriticalSMSAfter
	}
	return defaultCriticalSMSAfter
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// HandleDeliveryRequested processes one notify.DeliveryRequested event.
// eventID is the envelope id used for processed_events deduplication.
// Resolve runs before MarkProcessed so a temporary Recipients failure can
// retry without burning the dedup slot. Resolve must not look up phones: a
// Kratos outage must not lose the event. Push and immediate external SMS are
// scheduled as platform timers in the same transaction; providers and
// PhoneFor run in the timer handlers after commit (never inside the consumer txn).
func (s *Service) HandleDeliveryRequested(ctx context.Context, eventID uuid.UUID, req model.DeliveryRequest) error {
	if eventID == uuid.Nil {
		return fmt.Errorf("%w: event id", model.ErrInvalidInput)
	}
	if err := req.Validate(); err != nil {
		return err
	}

	recipients, err := s.Recipients.Resolve(ctx, req.Recipient)
	if err != nil {
		return fmt.Errorf("resolve recipient: %w", err)
	}
	if len(recipients) == 0 {
		return model.ErrRecipientUnresolved
	}
	for _, r := range recipients {
		if err := r.Validate(); err != nil {
			return err
		}
	}

	return s.Store.InTx(ctx, func(tx port.Tx) error {
		newly, err := tx.MarkProcessed(ctx, model.ConsumerDeliveryRequested, eventID)
		if err != nil {
			return err
		}
		if !newly {
			s.log().Info("notify event already processed", "event", "DeliveryRequested", "event_id", eventID)
			return nil
		}

		now := s.Clock.Now().UTC()
		for _, r := range recipients {
			n := model.Notification{
				RequestID:     req.RequestID,
				RecipientKind: r.Kind,
				RecipientRef:  r.Ref,
				Template:      req.Template,
				Params:        req.Params,
				Class:         req.Class,
				CreatedAt:     now,
			}
			created, inserted, err := tx.InsertNotification(ctx, n)
			if err != nil {
				return err
			}
			if !inserted {
				created, err = tx.GetNotificationByRequestRecipient(ctx, req.RequestID, r.Kind, r.Ref)
				if err != nil {
					return err
				}
			}

			if model.IsSMSOnly(r.Kind) {
				if _, _, err := tx.InsertDelivery(ctx, created.ID, model.ChannelSMS, model.StatusPending, now); err != nil {
					return err
				}
				if inserted {
					if err := tx.ScheduleExternalSMS(ctx, created.ID, now); err != nil {
						return err
					}
					s.log().Info("external sms timer scheduled", "event", "ExternalSMSScheduled", "notification_id", created.ID)
				}
				continue
			}

			if _, _, err := tx.InsertDelivery(ctx, created.ID, model.ChannelPush, model.StatusPending, now); err != nil {
				return err
			}
			if !inserted {
				continue
			}
			if err := tx.SchedulePush(ctx, created.ID, now); err != nil {
				return err
			}
			if req.Class == model.ClassCritical {
				due := now.Add(s.criticalAfter())
				if err := tx.ScheduleCriticalSMS(ctx, created.ID, due); err != nil {
					return err
				}
				s.log().Info("critical sms timer scheduled", "event", "CriticalSMSScheduled", "notification_id", created.ID)
			}
		}
		return nil
	})
}

// HandlePush sends push for a member notification and updates the delivery row.
// Idempotent on delivery status: a prior successful run leaves status=sent and
// skips the provider. At-least-once edge: if the provider accepts the push but
// updating the delivery row fails, a timer retry may send again.
func (s *Service) HandlePush(ctx context.Context, notificationID uuid.UUID) error {
	if notificationID == uuid.Nil {
		return fmt.Errorf("%w: notification id", model.ErrInvalidInput)
	}

	var (
		n       model.Notification
		devices []model.Device
		skip    bool
	)
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		n, err = tx.GetNotification(ctx, notificationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, model.ErrNotFound) {
				skip = true
				return nil
			}
			return err
		}
		if n.RecipientKind != model.RecipientKindMember {
			skip = true
			return nil
		}
		_, status, _, err := tx.GetDelivery(ctx, n.ID, model.ChannelPush)
		if err != nil {
			if errors.Is(err, model.ErrNotFound) {
				skip = true
				return nil
			}
			return err
		}
		if status == model.StatusSent || status == model.StatusSkipped {
			skip = true
			return nil
		}
		devices, err = tx.ListDevices(ctx, n.RecipientRef)
		return err
	})
	if err != nil {
		return err
	}
	if skip {
		return nil
	}
	return s.finishPush(ctx, n, devices)
}

func (s *Service) finishPush(ctx context.Context, n model.Notification, devices []model.Device) error {
	now := s.Clock.Now().UTC()
	if len(devices) == 0 || s.Push == nil {
		return s.Store.InTx(ctx, func(tx port.Tx) error {
			id, status, attempts, err := tx.GetDelivery(ctx, n.ID, model.ChannelPush)
			if err != nil {
				return err
			}
			if status == model.StatusSent || status == model.StatusSkipped {
				return nil
			}
			return tx.UpdateDelivery(ctx, id, model.StatusSkipped, attempts, nil, now)
		})
	}

	var lastErr error
	var providerID string
	sent := false
	for _, d := range devices {
		pid, err := s.Push.Send(ctx, port.PushMessage{
			MemberID:       d.MemberID,
			DeviceID:       d.DeviceID,
			PushToken:      d.PushToken,
			Platform:       d.Platform,
			NotificationID: n.ID,
			Template:       n.Template,
			Params:         n.Params,
			Class:          n.Class,
		})
		if err != nil {
			lastErr = err
			s.log().Error("push send failed", "event", "PushFailed", "notification_id", n.ID, "device_id", d.DeviceID, "error", err)
			continue
		}
		providerID = pid
		sent = true
		break
	}

	return s.Store.InTx(ctx, func(tx port.Tx) error {
		id, status, attempts, err := tx.GetDelivery(ctx, n.ID, model.ChannelPush)
		if err != nil {
			return err
		}
		if status == model.StatusSent || status == model.StatusSkipped {
			return nil
		}
		attempts++
		if sent {
			var pid *string
			if providerID != "" {
				pid = &providerID
			}
			return tx.UpdateDelivery(ctx, id, model.StatusSent, attempts, pid, now)
		}
		_ = lastErr
		return tx.UpdateDelivery(ctx, id, model.StatusFailed, attempts, nil, now)
	})
}

// HandleExternalSMS sends SMS immediately for an external recipient (any class).
// Phone is looked up via PhoneFor at send time, outside any database
// transaction, and never stored. Idempotent on delivery status. At-least-once
// edge: provider accept + failed status update may cause a duplicate send on
// timer retry.
func (s *Service) HandleExternalSMS(ctx context.Context, notificationID uuid.UUID) error {
	return s.sendSMS(ctx, notificationID, smsModeExternal)
}

// HandleCriticalSMS is the timer handler for model.TimerCriticalSMS (member
// recipients only). It sends at most one SMS when still unacknowledged.
// Critical SMS uses the guard without the daily budget breaker; other guard
// rejections are logged as CriticalSMSBlocked and marked failed. A PhoneFor
// failure fails the timer run so it retries; it never drops the SMS silently.
func (s *Service) HandleCriticalSMS(ctx context.Context, notificationID uuid.UUID) error {
	return s.sendSMS(ctx, notificationID, smsModeCritical)
}

type smsMode int

const (
	smsModeCritical smsMode = iota
	smsModeExternal
)

func (s *Service) sendSMS(ctx context.Context, notificationID uuid.UUID, mode smsMode) error {
	if notificationID == uuid.Nil {
		return fmt.Errorf("%w: notification id", model.ErrInvalidInput)
	}

	var (
		n          model.Notification
		skipReason string
		deliveryID uuid.UUID
		attempts   int32
		ready      bool
		failGuard  bool
	)

	// Load and claim the delivery inside a short transaction. Phone lookup and
	// the provider call stay outside so a Kratos outage does not hold a pool
	// connection and can retry without losing the notification.
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		n, err = tx.GetNotification(ctx, notificationID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, model.ErrNotFound) {
				skipReason = "missing"
				return nil
			}
			return err
		}

		switch mode {
		case smsModeCritical:
			if n.RecipientKind != model.RecipientKindMember {
				skipReason = "not_member"
				return nil
			}
			if n.Class != model.ClassCritical {
				skipReason = "not_critical"
				return nil
			}
			if n.AcknowledgedAt != nil {
				skipReason = "acknowledged"
				return nil
			}
		case smsModeExternal:
			if !model.IsSMSOnly(n.RecipientKind) {
				skipReason = "not_external"
				return nil
			}
		}

		now := s.Clock.Now().UTC()
		id, inserted, err := tx.InsertDelivery(ctx, n.ID, model.ChannelSMS, model.StatusPending, now)
		if err != nil {
			return err
		}
		if !inserted {
			existingID, status, existingAttempts, err := tx.GetDelivery(ctx, n.ID, model.ChannelSMS)
			if err != nil {
				return err
			}
			if status == model.StatusSent || status == model.StatusSkipped {
				skipReason = "already_" + status
				return nil
			}
			deliveryID = existingID
			attempts = existingAttempts
		} else {
			deliveryID = id
		}
		ready = true
		return nil
	})
	if err != nil {
		return err
	}
	if !ready {
		event := "CriticalSMSSkipped"
		if mode == smsModeExternal {
			event = "ExternalSMSSkipped"
		}
		s.log().Info("sms skipped", "event", event, "notification_id", notificationID, "reason", skipReason)
		return nil
	}

	resolved, err := s.Recipients.Resolve(ctx, model.RecipientForm(n.RecipientKind, n.RecipientRef))
	if err != nil {
		return fmt.Errorf("resolve recipient for sms: %w", err)
	}
	lang := "ru"
	if len(resolved) > 0 && resolved[0].Language != "" {
		lang = resolved[0].Language
	}

	phone, err := s.Recipients.PhoneFor(ctx, n.RecipientKind, n.RecipientRef)
	if err != nil {
		// Leave delivery pending so the timer retries with backoff.
		return fmt.Errorf("phone for sms: %w", err)
	}
	if phone == "" {
		_ = s.Store.InTx(ctx, func(tx port.Tx) error {
			return tx.UpdateDelivery(ctx, deliveryID, model.StatusSkipped, attempts, nil, s.Clock.Now().UTC())
		})
		event := "CriticalSMSSkipped"
		if mode == smsModeExternal {
			event = "ExternalSMSSkipped"
		}
		s.log().Info("sms skipped", "event", event, "notification_id", notificationID, "reason", "no_phone")
		return nil
	}

	text := s.smsText(n.Template, lang)
	if text == "" {
		_ = s.Store.InTx(ctx, func(tx port.Tx) error {
			return tx.UpdateDelivery(ctx, deliveryID, model.StatusSkipped, attempts, nil, s.Clock.Now().UTC())
		})
		event := "CriticalSMSSkipped"
		if mode == smsModeExternal {
			event = "ExternalSMSSkipped"
		}
		s.log().Info("sms skipped", "event", event, "notification_id", notificationID, "reason", "no_text")
		return nil
	}

	critical := n.Class == model.ClassCritical || mode == smsModeCritical
	if s.Guard != nil {
		var guardErr error
		if critical {
			guardErr = s.Guard.AllowCritical(phone, s.Clock.Now())
		} else {
			guardErr = s.Guard.Allow(phone, s.Clock.Now())
		}
		if guardErr != nil {
			reason := guard.Explain(guardErr)
			_ = s.Store.InTx(ctx, func(tx port.Tx) error {
				status := model.StatusSkipped
				if critical {
					status = model.StatusFailed
					failGuard = true
				}
				return tx.UpdateDelivery(ctx, deliveryID, status, attempts+1, nil, s.Clock.Now().UTC())
			})
			if failGuard {
				s.log().Error("critical sms blocked by guard", "event", "CriticalSMSBlocked", "notification_id", notificationID, "reason", reason)
				return nil
			}
			s.log().Info("sms skipped", "event", "ExternalSMSSkipped", "notification_id", notificationID, "reason", "guard")
			return nil
		}
	}

	if s.SMS == nil {
		return errors.New("notify: sms sender not configured")
	}
	providerID, err := s.SMS.Send(ctx, port.SMSMessage{
		NotificationID: n.ID,
		PhoneE164:      phone,
		Text:           text,
	})
	now := s.Clock.Now().UTC()
	attempts++
	if err != nil {
		_ = s.Store.InTx(ctx, func(tx port.Tx) error {
			return tx.UpdateDelivery(ctx, deliveryID, model.StatusFailed, attempts, nil, now)
		})
		s.log().Error("sms send failed", "event", "SMSFailed", "notification_id", n.ID, "error", err)
		return err
	}
	var pid *string
	if providerID != "" {
		pid = &providerID
	}
	if err := s.Store.InTx(ctx, func(tx port.Tx) error {
		return tx.UpdateDelivery(ctx, deliveryID, model.StatusSent, attempts, pid, now)
	}); err != nil {
		return err
	}
	event := "CriticalSMSSent"
	if mode == smsModeExternal {
		event = "ExternalSMSSent"
	}
	s.log().Info("sms sent", "event", event, "notification_id", n.ID)
	return nil
}

func (s *Service) smsText(template, lang string) string {
	if lang == "" {
		lang = "ru"
	}
	byLang := s.Config.SMSTexts[template]
	if byLang == nil {
		return ""
	}
	if t := byLang[lang]; t != "" {
		return t
	}
	if t := byLang["ru"]; t != "" {
		return t
	}
	if t := byLang["kk"]; t != "" {
		return t
	}
	return ""
}

// ListInbox returns the member's notifications, newest first.
func (s *Service) ListInbox(ctx context.Context, memberID uuid.UUID, cursor string, limit int) (model.InboxPage, error) {
	if memberID == uuid.Nil {
		return model.InboxPage{}, model.ErrUnauthorized
	}
	if limit <= 0 {
		limit = defaultInboxLimit
	}
	if limit > maxInboxLimit {
		limit = maxInboxLimit
	}

	var cursorAt *time.Time
	var cursorID *uuid.UUID
	if cursor != "" {
		at, id, err := decodeCursor(cursor)
		if err != nil {
			return model.InboxPage{}, err
		}
		cursorAt = &at
		cursorID = &id
	}

	pageLimit := int32(limit) // limit is clamped to maxInboxLimit (50)
	var items []model.Notification
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		items, err = tx.ListInbox(ctx, memberID, cursorAt, cursorID, pageLimit)
		return err
	})
	if err != nil {
		return model.InboxPage{}, err
	}

	page := model.InboxPage{Items: items}
	if len(items) == limit {
		last := items[len(items)-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

// MarkRead marks a notification read; for critical it also acknowledges
// (SQL sets acknowledged_at) and cancels the SMS timer in the same
// transaction so a crash cannot leave an acknowledgement without cancelling
// the fallback SMS.
func (s *Service) MarkRead(ctx context.Context, memberID, notificationID uuid.UUID) error {
	if memberID == uuid.Nil {
		return model.ErrUnauthorized
	}
	now := s.Clock.Now().UTC()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		n, err := tx.MarkRead(ctx, notificationID, memberID, now)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return model.ErrNotFound
			}
			return err
		}
		if n.Class == model.ClassCritical {
			if _, err := tx.CancelCriticalSMS(ctx, notificationID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// RegisterDevice upserts a push device for the member.
func (s *Service) RegisterDevice(ctx context.Context, memberID, deviceID uuid.UUID, platform, pushToken string) error {
	if memberID == uuid.Nil {
		return model.ErrUnauthorized
	}
	if deviceID == uuid.Nil {
		return fmt.Errorf("%w: device id", model.ErrInvalidInput)
	}
	if !model.PlatformIsValid(platform) {
		return fmt.Errorf("%w: platform", model.ErrInvalidInput)
	}
	if len(pushToken) < 8 || len(pushToken) > 4096 {
		return fmt.Errorf("%w: push token", model.ErrInvalidInput)
	}
	now := s.Clock.Now().UTC()
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		return tx.UpsertDevice(ctx, model.Device{
			MemberID:  memberID,
			DeviceID:  deviceID,
			Platform:  platform,
			PushToken: pushToken,
			UpdatedAt: now,
		})
	})
}

// UnregisterDevice removes a device; missing is success.
func (s *Service) UnregisterDevice(ctx context.Context, memberID, deviceID uuid.UUID) error {
	if memberID == uuid.Nil {
		return model.ErrUnauthorized
	}
	return s.Store.InTx(ctx, func(tx port.Tx) error {
		return tx.DeleteDevice(ctx, memberID, deviceID)
	})
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, model.ErrInvalidCursor
	}
	parts := strings.Split(string(b), "|")
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, model.ErrInvalidCursor
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, uuid.Nil, model.ErrInvalidCursor
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, model.ErrInvalidCursor
	}
	return at, id, nil
}

// ParseDeliveryRequested decodes the event data object (not the envelope).
func ParseDeliveryRequested(data json.RawMessage) (model.DeliveryRequest, error) {
	var raw struct {
		RequestID uuid.UUID `json:"request_id"`
		Recipient struct {
			MemberID         *uuid.UUID `json:"member_id"`
			TrustedContactID *uuid.UUID `json:"trusted_contact_id"`
			StaffRole        string     `json:"staff_role"`
		} `json:"recipient"`
		Class    string            `json:"class"`
		Template string            `json:"template"`
		Params   map[string]string `json:"params"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return model.DeliveryRequest{}, fmt.Errorf("%w: %w", model.ErrInvalidInput, err)
	}
	req := model.DeliveryRequest{
		RequestID: raw.RequestID,
		Recipient: model.Recipient{
			MemberID:         raw.Recipient.MemberID,
			TrustedContactID: raw.Recipient.TrustedContactID,
			StaffRole:        raw.Recipient.StaffRole,
		},
		Class:    raw.Class,
		Template: raw.Template,
		Params:   raw.Params,
	}
	if req.Params == nil {
		req.Params = map[string]string{}
	}
	if err := req.Validate(); err != nil {
		return model.DeliveryRequest{}, err
	}
	return req, nil
}
