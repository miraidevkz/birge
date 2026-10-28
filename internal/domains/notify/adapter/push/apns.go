package push

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"birge/internal/domains/notify/port"
	"birge/internal/platform/clock"
)

const (
	// DefaultAPNsBaseURL is the production APNs host.
	DefaultAPNsBaseURL = "https://api.push.apple.com"
	// APNsSandboxBaseURL is the development APNs host; tokens from a debug
	// build only work there (production answers BadDeviceToken).
	APNsSandboxBaseURL = "https://api.sandbox.push.apple.com"
	// providerTokenTTL is when a new provider token is signed. Apple rejects
	// tokens older than 60 minutes and answers TooManyProviderTokenUpdates
	// when a new one arrives more often than every 20; 40 sits in between.
	providerTokenTTL = 40 * time.Minute
	// providerTokenMinAge is the youngest token replaced after an
	// ExpiredProviderToken reply (a clock skew), to stay out of the
	// TooManyProviderTokenUpdates window.
	providerTokenMinAge = 20 * time.Minute
)

var appleID = regexp.MustCompile(`^[A-Z0-9]{10}$`)

// APNsCredentials is a token-auth key: the .p8 private key with its key ID
// and team ID. It holds the private key: never log it (String redacts it).
type APNsCredentials struct {
	KeyID  string
	TeamID string
	key    *ecdsa.PrivateKey
}

// String redacts the credentials.
func (APNsCredentials) String() string { return "push.APNsCredentials{redacted}" }

// GoString redacts the credentials for %#v.
func (APNsCredentials) GoString() string { return "push.APNsCredentials{redacted}" }

// ParseAPNsKey reads a .p8 auth key and checks the key and team IDs (ten
// upper-case letters or digits each, as Apple issues them).
func ParseAPNsKey(p8 []byte, keyID, teamID string) (APNsCredentials, error) {
	if !appleID.MatchString(keyID) {
		return APNsCredentials{}, errors.New("apns: the key ID must be 10 upper-case letters or digits")
	}
	if !appleID.MatchString(teamID) {
		return APNsCredentials{}, errors.New("apns: the team ID must be 10 upper-case letters or digits")
	}
	key, err := parseP8Key(p8)
	if err != nil {
		return APNsCredentials{}, fmt.Errorf("apns: %w", err)
	}
	return APNsCredentials{KeyID: keyID, TeamID: teamID, key: key}, nil
}

// APNs sends iOS pushes with token-based auth over HTTP/2. It is safe for
// concurrent use; the provider token is shared and renewed under a mutex.
type APNs struct {
	Credentials APNsCredentials
	// Topic is the app's bundle ID (apns-topic).
	Topic string
	// BaseURL is DefaultAPNsBaseURL or APNsSandboxBaseURL (tests: httptest).
	BaseURL string
	// Client must speak HTTP/2; the default client does over TLS.
	Client *http.Client
	Clock  clock.Clock

	mu     sync.Mutex
	jwt    string
	issued time.Time
}

var _ port.PushSender = (*APNs)(nil)

// Send delivers one alert push. Only the notification ID and Title go on the wire.
func (a *APNs) Send(ctx context.Context, msg port.PushMessage) (string, error) {
	if msg.PushToken == "" {
		return "", fmt.Errorf("apns: empty token: %w", port.ErrTokenInvalid)
	}
	jwt, err := a.providerToken()
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(map[string]any{
		"aps": map[string]any{
			"alert": map[string]string{"title": msg.Title},
			"sound": "default",
		},
		"notification_id": msg.NotificationID.String(),
	})
	if err != nil {
		return "", fmt.Errorf("apns: encode: %w", err)
	}
	base := a.BaseURL
	if base == "" {
		base = DefaultAPNsBaseURL
	}
	target := strings.TrimRight(base, "/") + "/3/device/" + url.PathEscape(msg.PushToken)
	status, header, body, err := do(ctx, clientOrDefault(a.Client), http.MethodPost, target, http.Header{
		"Authorization":  {"bearer " + jwt},
		"Apns-Topic":     {a.Topic},
		"Apns-Push-Type": {"alert"},
		// One priority for every class, so the class does not leak.
		"Apns-Priority": {"10"},
		"Apns-Id":       {msg.NotificationID.String()},
		"Content-Type":  {"application/json"},
	}, payload)
	if err != nil {
		return "", fmt.Errorf("apns: %w", err)
	}
	if status == http.StatusOK {
		return header.Get("Apns-Id"), nil
	}
	var reply struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &reply)
	if reply.Reason == "ExpiredProviderToken" {
		a.dropToken(jwt)
	}
	return "", classifyAPNs(status, reply.Reason)
}

// classifyAPNs maps an APNs error reply onto the port error classes
// (Apple: "Handling notification responses from APNs").
func classifyAPNs(status int, reason string) error {
	base := fmt.Sprintf("apns: status %d %s", status, safeCode(reason))
	switch {
	case status == http.StatusGone,
		reason == "Unregistered", reason == "BadDeviceToken", reason == "DeviceTokenNotForTopic":
		return fmt.Errorf("%s: %w", base, port.ErrTokenInvalid)
	case retryable(status), status == http.StatusForbidden,
		// Topic problems are our configuration, fixed by an operator.
		reason == "BadTopic", reason == "TopicDisallowed", reason == "MissingTopic":
		return errors.New(base)
	default:
		return fmt.Errorf("%s: %w", base, port.ErrPermanent)
	}
}

// providerToken returns the cached ES256 provider token, signing a new one
// once the cached one is providerTokenTTL old.
func (a *APNs) providerToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := clockOrSystem(a.Clock).Now()
	if a.jwt != "" && now.Sub(a.issued) < providerTokenTTL {
		return a.jwt, nil
	}
	if a.Credentials.key == nil {
		return "", errors.New("apns: no auth key")
	}
	jwt, err := signJWT(
		map[string]string{"alg": "ES256", "kid": a.Credentials.KeyID},
		map[string]any{"iss": a.Credentials.TeamID, "iat": now.Unix()},
		es256(a.Credentials.key),
	)
	if err != nil {
		return "", fmt.Errorf("apns: sign provider token: %w", err)
	}
	a.jwt, a.issued = jwt, now
	return jwt, nil
}

// dropToken forgets the provider token Apple called expired, unless it is
// younger than providerTokenMinAge (renewing that early would be refused).
func (a *APNs) dropToken(failed string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.jwt == failed && clockOrSystem(a.Clock).Now().Sub(a.issued) >= providerTokenMinAge {
		a.jwt = ""
	}
}
