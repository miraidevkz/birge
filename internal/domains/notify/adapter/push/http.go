package push

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"birge/internal/platform/clock"
)

// maxResponseBody bounds what is read from a provider response: replies are
// small JSON objects, and an unbounded read would let a broken proxy exhaust
// memory.
const maxResponseBody = 64 << 10

// defaultTimeout bounds one provider request when the caller's context has
// no earlier deadline, so a hung connection cannot stall a timer worker.
const defaultTimeout = 15 * time.Second

func clientOrDefault(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	// http.DefaultTransport negotiates HTTP/2 over TLS, which APNs requires.
	return &http.Client{Timeout: defaultTimeout}
}

func clockOrSystem(c clock.Clock) clock.Clock {
	if c != nil {
		return c
	}
	return clock.System{}
}

// do sends one request and returns status, headers and a bounded body. A
// transport error is unwrapped from *url.Error first: its text contains the
// request URL, and the APNs URL contains the device token.
func do(ctx context.Context, client *http.Client, method, target string, header http.Header, body []byte) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, nil, nil, fmt.Errorf("build request: %w", err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, nil, nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, resp.Header, b, nil
}

// safeCode keeps a provider reason code printable and short, so a reply that
// echoes request data cannot carry a token into logs through an error text.
func safeCode(s string) string {
	const maxLen = 64
	out := make([]byte, 0, min(len(s), maxLen))
	for i := 0; i < len(s) && len(out) < maxLen; i++ {
		c := s[i]
		if c == '_' || c == '-' || c == '.' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		}
	}
	return string(out)
}

// retryable reports the statuses every provider asks callers to retry later.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}
