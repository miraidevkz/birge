package objectstore

import (
	"context"
	"errors"
	"time"
)

// ErrNotConfigured means object storage is not configured for this process.
// Photo upload handlers map it to 503 storage_not_configured.
var ErrNotConfigured = errors.New("objectstore: not configured")

// Store is the portable S3 subset used by Birge (ADR-0015).
type Store interface {
	// PresignPut returns a temporary URL the client may PUT raw bytes to.
	// The signature requires the given Content-Type and Content-Length.
	PresignPut(ctx context.Context, key, contentType string, contentLength int64, expires time.Duration) (url string, err error)
	// PresignGet returns a URL for reading an object. When the store was
	// built with a public base URL, that URL is returned instead of a
	// signed one (processed photos).
	PresignGet(ctx context.Context, key string, expires time.Duration) (url string, err error)
	// Get returns the object bytes.
	Get(ctx context.Context, key string) ([]byte, error)
	// Put stores bytes under key with the given content type.
	Put(ctx context.Context, key string, body []byte, contentType string) error
	// Delete removes the object; a missing key is not an error.
	Delete(ctx context.Context, key string) error
}
