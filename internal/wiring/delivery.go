package wiring

import (
	"errors"
	"log/slog"
	"slices"

	pushadapter "birge/internal/domains/notify/adapter/push"
	smsadapter "birge/internal/domains/notify/adapter/smsgw"
	notifyport "birge/internal/domains/notify/port"
)

// DeliverySenders builds the push and SMS senders cfg names. Load already
// refuses the log-only senders outside dev mode; this checks again because
// silently dropping notifications (an SOS among them) is the failure the
// rule exists for, and a Config can be built without Load.
func DeliverySenders(cfg Config, logger *slog.Logger) (notifyport.PushSender, notifyport.SMSSender, error) {
	dev := cfg.DeliveryMode == DeliveryModeDev
	var push notifyport.PushSender
	switch {
	case slices.Equal(cfg.PushProviders, []string{ProviderLog}):
		if !dev {
			return nil, nil, errors.New("log-only push sender outside dev mode")
		}
		logger.Warn("log-only push: pushes are logged, not sent", "event", "LogOnlyPush")
		push = LogPushSender{Logger: logger}
	case len(cfg.PushProviders) > 0:
		var r pushadapter.Router
		if slices.Contains(cfg.PushProviders, ProviderFCM) {
			r.Android = &pushadapter.FCM{Credentials: cfg.FCM}
		}
		if slices.Contains(cfg.PushProviders, ProviderAPNs) {
			r.IOS = &pushadapter.APNs{Credentials: cfg.APNs, Topic: cfg.APNsTopic, BaseURL: cfg.APNsBaseURL}
		}
		push = r
	default:
		return nil, nil, errors.New("no push provider configured")
	}

	var sms notifyport.SMSSender
	switch cfg.SMSProvider {
	case ProviderLog:
		if !dev {
			return nil, nil, errors.New("log-only SMS sender outside dev mode")
		}
		logger.Warn("log-only SMS: SMS are logged, not sent", "event", "LogOnlySMS")
		sms = LogSMSSender{Logger: logger}
	case ProviderSMSC:
		sms = &smsadapter.HTTPSender{Provider: cfg.SMSC}
	default:
		return nil, nil, errors.New("no SMS provider configured")
	}
	return push, sms, nil
}
