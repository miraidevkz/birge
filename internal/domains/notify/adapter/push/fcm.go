package push

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"birge/internal/domains/notify/port"
	"birge/internal/platform/clock"
)

const (
	// DefaultFCMBaseURL is the FCM HTTP v1 API host.
	DefaultFCMBaseURL = "https://fcm.googleapis.com"
	// DefaultGoogleTokenURL is used when the service-account file names no token_uri.
	DefaultGoogleTokenURL = "https://oauth2.googleapis.com/token" //nolint:gosec // G101: a public endpoint, not a credential
	fcmScope              = "https://www.googleapis.com/auth/firebase.messaging"
	// assertionLifetime is the longest lifetime Google accepts for the
	// self-signed assertion; the access token it buys lives about as long.
	assertionLifetime = time.Hour
	// tokenRefreshMargin renews the access token this long before it
	// expires, so a request never starts with a token about to lapse.
	tokenRefreshMargin = 5 * time.Minute
)

// FCMCredentials is the part of a Google service-account file FCM needs.
// It holds the private key: never log it (String redacts it).
type FCMCredentials struct {
	ProjectID    string
	ClientEmail  string
	PrivateKeyID string
	TokenURI     string
	key          *rsa.PrivateKey
}

// String redacts the credentials.
func (FCMCredentials) String() string { return "push.FCMCredentials{redacted}" }

// GoString redacts the credentials for %#v.
func (FCMCredentials) GoString() string { return "push.FCMCredentials{redacted}" }

// ParseFCMServiceAccount reads a Google service-account JSON key file. Errors
// name the missing field, never a value.
func ParseFCMServiceAccount(b []byte) (FCMCredentials, error) {
	var raw struct {
		Type         string `json:"type"`
		ProjectID    string `json:"project_id"`
		PrivateKeyID string `json:"private_key_id"`
		PrivateKey   string `json:"private_key"`
		ClientEmail  string `json:"client_email"`
		TokenURI     string `json:"token_uri"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return FCMCredentials{}, errors.New("fcm: the service-account file is not JSON")
	}
	if raw.Type != "service_account" {
		return FCMCredentials{}, errors.New(`fcm: the service-account file must have "type": "service_account"`)
	}
	for _, f := range []struct{ name, v string }{
		{"project_id", raw.ProjectID}, {"client_email", raw.ClientEmail}, {"private_key", raw.PrivateKey},
	} {
		if strings.TrimSpace(f.v) == "" {
			return FCMCredentials{}, fmt.Errorf("fcm: the service-account file has no %s", f.name)
		}
	}
	key, err := parseRSAKey([]byte(raw.PrivateKey))
	if err != nil {
		return FCMCredentials{}, fmt.Errorf("fcm: private_key: %w", err)
	}
	tokenURI := raw.TokenURI
	if tokenURI == "" {
		tokenURI = DefaultGoogleTokenURL
	}
	if u, err := url.Parse(tokenURI); err != nil || u.Scheme != "https" || u.Host == "" {
		return FCMCredentials{}, errors.New("fcm: token_uri must be an https URL")
	}
	return FCMCredentials{
		ProjectID:    raw.ProjectID,
		ClientEmail:  raw.ClientEmail,
		PrivateKeyID: raw.PrivateKeyID,
		TokenURI:     tokenURI,
		key:          key,
	}, nil
}

// FCM sends Android pushes through the FCM HTTP v1 API. It is safe for
// concurrent use; the OAuth2 access token is shared and renewed under a
// mutex. The zero values of BaseURL, TokenURL, Client and Clock mean the
// production defaults.
type FCM struct {
	Credentials FCMCredentials
	// BaseURL overrides DefaultFCMBaseURL (tests point it at httptest).
	BaseURL string
	// TokenURL overrides Credentials.TokenURI (tests).
	TokenURL string
	Client   *http.Client
	Clock    clock.Clock

	mu      sync.Mutex
	token   string
	expires time.Time
}

var _ port.PushSender = (*FCM)(nil)

// Send delivers one push. Only the notification ID and Title go on the wire.
func (f *FCM) Send(ctx context.Context, msg port.PushMessage) (string, error) {
	if msg.PushToken == "" {
		return "", fmt.Errorf("fcm: empty token: %w", port.ErrTokenInvalid)
	}
	token, err := f.accessToken(ctx)
	if err != nil {
		return "", err
	}
	var body struct {
		Message fcmMessage `json:"message"`
	}
	body.Message = fcmMessage{
		Token:        msg.PushToken,
		Notification: fcmNotification{Title: msg.Title},
		Data:         map[string]string{"notification_id": msg.NotificationID.String()},
		// One priority for every class: a class-dependent priority would
		// tell the provider which notifications are critical.
		Android: fcmAndroid{Priority: "HIGH"},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("fcm: encode: %w", err)
	}
	base := f.BaseURL
	if base == "" {
		base = DefaultFCMBaseURL
	}
	target := strings.TrimRight(base, "/") + "/v1/projects/" + url.PathEscape(f.Credentials.ProjectID) + "/messages:send"
	status, _, respBody, err := do(ctx, clientOrDefault(f.Client), http.MethodPost, target, http.Header{
		"Authorization": {"Bearer " + token},
		"Content-Type":  {"application/json; charset=UTF-8"},
	}, payload)
	if err != nil {
		return "", fmt.Errorf("fcm: %w", err)
	}
	if status == http.StatusOK {
		var ok struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(respBody, &ok)
		return ok.Name, nil
	}
	if status == http.StatusUnauthorized {
		// The access token was revoked or expired early: fetch a new one
		// on the next attempt.
		f.dropToken(token)
	}
	return "", classifyFCM(status, respBody)
}

type fcmMessage struct {
	Token        string            `json:"token"`
	Notification fcmNotification   `json:"notification"`
	Data         map[string]string `json:"data"`
	Android      fcmAndroid        `json:"android"`
}

type fcmNotification struct {
	Title string `json:"title"`
}

type fcmAndroid struct {
	Priority string `json:"priority"`
}

// classifyFCM maps an FCM v1 error reply onto the port error classes
// (https://firebase.google.com/docs/reference/fcm/rest/v1/ErrorCode).
func classifyFCM(status int, body []byte) error {
	var reply struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				ErrorCode       string `json:"errorCode"`
				FieldViolations []struct {
					Field string `json:"field"`
				} `json:"fieldViolations"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &reply)
	code := reply.Error.Status
	tokenField := false
	for _, d := range reply.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
		for _, v := range d.FieldViolations {
			if v.Field == "message.token" {
				tokenField = true
			}
		}
	}
	base := fmt.Sprintf("fcm: status %d %s", status, safeCode(code))
	switch {
	case code == "UNREGISTERED" || code == "SENDER_ID_MISMATCH" || status == http.StatusNotFound:
		return fmt.Errorf("%s: %w", base, port.ErrTokenInvalid)
	case status == http.StatusBadRequest && tokenField:
		// INVALID_ARGUMENT naming message.token: the token is malformed.
		return fmt.Errorf("%s: %w", base, port.ErrTokenInvalid)
	case retryable(status), status == http.StatusUnauthorized, status == http.StatusForbidden:
		// 401/403 are our credentials or project permissions: an operator
		// fixes them and the timer retry then delivers.
		return errors.New(base)
	default:
		return fmt.Errorf("%s: %w", base, port.ErrPermanent)
	}
}

// accessToken returns a cached OAuth2 token, renewing it shortly before it
// expires. The mutex is held across the token request so concurrent sends
// wait for one renewal instead of each signing their own assertion.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := clockOrSystem(f.Clock).Now()
	if f.token != "" && now.Before(f.expires) {
		return f.token, nil
	}
	if f.Credentials.key == nil {
		return "", errors.New("fcm: no service-account credentials")
	}
	tokenURL := f.TokenURL
	if tokenURL == "" {
		tokenURL = f.Credentials.TokenURI
	}
	assertion, err := signJWT(
		map[string]string{"alg": "RS256", "typ": "JWT", "kid": f.Credentials.PrivateKeyID},
		map[string]any{
			"iss":   f.Credentials.ClientEmail,
			"scope": fcmScope,
			"aud":   tokenURL,
			"iat":   now.Unix(),
			"exp":   now.Add(assertionLifetime).Unix(),
		},
		rs256(f.Credentials.key),
	)
	if err != nil {
		return "", fmt.Errorf("fcm: sign assertion: %w", err)
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	status, _, body, err := do(ctx, clientOrDefault(f.Client), http.MethodPost, tokenURL, http.Header{
		"Content-Type": {"application/x-www-form-urlencoded"},
	}, []byte(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("fcm: token: %w", err)
	}
	var reply struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
	}
	_ = json.Unmarshal(body, &reply)
	if status != http.StatusOK || reply.AccessToken == "" {
		// Always transient: a rejected assertion means a revoked key or a
		// skewed clock, both fixed by an operator, after which the timer
		// retry delivers.
		return "", fmt.Errorf("fcm: token: status %d %s", status, safeCode(reply.Error))
	}
	lifetime := time.Duration(reply.ExpiresIn) * time.Second
	if lifetime <= 0 {
		lifetime = assertionLifetime
	}
	f.token = reply.AccessToken
	f.expires = now.Add(lifetime - min(tokenRefreshMargin, lifetime/2))
	return f.token, nil
}

// dropToken forgets the cached token if it is still the one that failed.
func (f *FCM) dropToken(failed string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token == failed {
		f.token = ""
	}
}
