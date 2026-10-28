package smsgw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"birge/internal/domains/notify/port"
)

// maxResponseBody bounds the reply read; gateway replies are tiny.
const maxResponseBody = 64 << 10

// defaultTimeout bounds one request when the caller's context has no
// earlier deadline, so a hung gateway cannot stall a timer worker.
const defaultTimeout = 15 * time.Second

// Provider is one SMS gateway's wire format.
type Provider interface {
	// Name labels errors ("smsc").
	Name() string
	// Request builds the HTTP request for msg. Credentials and the phone go
	// in the body, never in the URL, so no proxy or access log keeps them.
	Request(ctx context.Context, msg port.SMSMessage) (*http.Request, error)
	// Parse reads a reply whose status is not 429 or 5xx. It returns the
	// gateway's message ID, or an error wrapping port.ErrPermanent when a
	// retry cannot help; any other error is retried.
	Parse(status int, body []byte) (providerMessageID string, err error)
}

// HTTPSender sends SMS through a Provider. It is safe for concurrent use
// when its Provider is (SMSC is: it holds configuration only).
type HTTPSender struct {
	Provider Provider
	// Client defaults to one with a 15-second timeout.
	Client *http.Client
}

var _ port.SMSSender = (*HTTPSender)(nil)

// Send implements port.SMSSender.
func (s *HTTPSender) Send(ctx context.Context, msg port.SMSMessage) (string, error) {
	name := s.Provider.Name()
	req, err := s.Provider.Request(ctx, msg)
	if err != nil {
		return "", fmt.Errorf("%s: build request: %w", name, stripURL(err))
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: request failed: %w", name, stripURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return "", fmt.Errorf("%s: read response: %w", name, err)
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return "", fmt.Errorf("%s: status %d", name, resp.StatusCode)
	}
	return s.Provider.Parse(resp.StatusCode, body)
}

// stripURL drops the *url.Error wrapper, whose text repeats the request URL.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
