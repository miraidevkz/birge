package wiring

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"birge/internal/domains/membership/authgw"
	notifyport "birge/internal/domains/notify/port"
	notifyservice "birge/internal/domains/notify/service"
)

// SignInCodes implements authgw.CodeSender with notify's synchronous
// SendSignInCode. It deliberately does not publish notify.DeliveryRequested:
// that event goes through the outbox and the stream and becomes a
// notification row, so the code would be stored in three places. The direct
// call keeps the code in memory only, and the SMS guard and provider of
// notify still apply. It translates notify's errors into the authgw classes
// so the hook can answer Kratos without importing notify.
type SignInCodes struct {
	Notify *notifyservice.Service
}

var _ authgw.CodeSender = SignInCodes{}

// SendSignInCode implements authgw.CodeSender.
func (s SignInCodes) SendSignInCode(ctx context.Context, phoneE164, code string) error {
	err := s.Notify.SendSignInCode(ctx, phoneE164, code)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, notifyservice.ErrSMSRefused):
		return fmt.Errorf("%w: %w", authgw.ErrCodeRefused, err)
	case errors.Is(err, notifyport.ErrPermanent):
		return fmt.Errorf("%w: %w", authgw.ErrCodePermanent, err)
	}
	return err
}

// InternalHandler is the worker's internal listener (BIRGE_INTERNAL_ADDR):
// cluster-internal endpoints that the ingress never routes. Today it serves
// only the Kratos courier hook at authgw.CourierPath; everything else is 404.
// It runs in the worker because the worker already holds the SMS provider
// credentials and the notify service; the API process never needs them.
func InternalHandler(cfg Config, notify *notifyservice.Service, logger *slog.Logger) (http.Handler, error) {
	hook, err := authgw.NewCourierHook(authgw.CourierConfig{
		Secret:          cfg.CourierSecret,
		AllowedPrefixes: cfg.SMSPrefixes,
		Sender:          SignInCodes{Notify: notify},
		Logger:          logger,
	})
	if err != nil {
		return nil, fmt.Errorf("wiring: courier hook: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle(authgw.CourierPath, hook)
	return mux, nil
}
