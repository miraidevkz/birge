package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/port"
	"birge/internal/domains/notify/sms/guard"
)

// ErrSMSRefused means the SMS guard refused a sign-in code SMS (number
// range, per-number or global rate limit, daily budget). The error wraps the
// guard's reason; neither carries the phone number or the code.
var ErrSMSRefused = errors.New("notify: sign-in SMS refused by the guard")

// SignInCodeText is the whole sign-in SMS: the product name and the code.
// It matches the Kratos courier templates (deploy/compose/kratos/kratos.yml)
// so the SMS reads the same whichever courier path sent it. The text Kratos
// rendered is never forwarded: building it here means nothing but the code
// can reach the provider (SMS carry no content, ADR-0013).
func SignInCodeText(code string) string { return "Birge: " + code }

// SendSignInCode sends one sign-in code by SMS for membership's Kratos
// courier hook (ADR-0008 addendum). It is synchronous on purpose and never
// touches the store, the outbox or a timer: a sign-in code is a credential,
// and anything that persists it (an event payload, notification params, a
// timer argument) keeps a secret at rest in the outbox, the event stream and
// notify's tables (the lesson of the plan link, w27). The code lives only in
// this call's memory and in the request to the provider. Kratos owns retries:
// an error here becomes a non-2xx answer and Kratos sends the message again
// later, so no Birge-side retry is needed.
//
// The SMS guard applies before the provider: SignInGuard, a guard of its own
// (same configuration, separate counters) so that sign-in pumping can never
// use up the per-number limit or the daily budget that a member's critical
// SMS or an external contact's plan SMS needs. A refusal is ErrSMSRefused.
// Provider errors are returned as they are (port.ErrPermanent or transient).
//
// The SMSMessage gets a fresh random ID in place of a notification ID: no
// notification row exists, and the ID only correlates the log lines of this
// send. Logs carry that ID and the outcome, never the phone or the code.
func (s *Service) SendSignInCode(ctx context.Context, phoneE164, code string) error {
	if !model.ValidSignInCode(code) {
		return fmt.Errorf("%w: sign-in code", model.ErrInvalidInput)
	}
	if s.SignInGuard == nil {
		return errors.New("notify: sign-in SMS guard not configured")
	}
	if s.SMS == nil {
		return errors.New("notify: sms sender not configured")
	}
	if err := s.SignInGuard.Allow(phoneE164, s.Clock.Now()); err != nil {
		s.log().Info("sign-in sms refused", "event", "SignInSMSRefused", "reason", guard.Explain(err))
		return fmt.Errorf("%w: %w", ErrSMSRefused, err)
	}
	id := uuid.New()
	_, err := s.SMS.Send(ctx, port.SMSMessage{NotificationID: id, PhoneE164: phoneE164, Text: SignInCodeText(code)})
	if err != nil {
		s.log().Error("sign-in sms failed", "event", "SignInSMSFailed", "sms_id", id,
			"permanent", errors.Is(err, port.ErrPermanent), "error", err)
		return err
	}
	s.log().Info("sign-in sms sent", "event", "SignInSMSSent", "sms_id", id)
	return nil
}
