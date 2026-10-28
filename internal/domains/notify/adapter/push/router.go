package push

import (
	"context"
	"fmt"

	"birge/internal/domains/notify/model"
	"birge/internal/domains/notify/port"
)

// Router sends each push through the provider for the device platform:
// Android to FCM, iOS to APNs. A nil field means that platform has no
// provider in this deployment (BIRGE_PUSH_PROVIDER lists only the other).
type Router struct {
	Android port.PushSender
	IOS     port.PushSender
}

var _ port.PushSender = Router{}

// Send implements port.PushSender. A platform without a provider is a
// permanent failure for that device, not a retry: nothing changes until the
// deployment is reconfigured, and the device registration stays.
func (r Router) Send(ctx context.Context, msg port.PushMessage) (string, error) {
	var s port.PushSender
	switch msg.Platform {
	case model.PlatformAndroid:
		s = r.Android
	case model.PlatformIOS:
		s = r.IOS
	}
	if s == nil {
		return "", fmt.Errorf("push: no provider for platform %q: %w", msg.Platform, port.ErrPermanent)
	}
	return s.Send(ctx, msg)
}
