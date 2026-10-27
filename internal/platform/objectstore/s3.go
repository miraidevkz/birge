package objectstore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config is the S3-compatible endpoint used by NewS3. Access keys are read
// from files by the wiring loader, never from the environment itself.
type Config struct {
	// Endpoint is host:port or an absolute http(s) URL of the S3 API.
	Endpoint  string
	Bucket    string
	Region    string
	AccessKey string
	SecretKey string
	// PublicBaseURL is the public origin for processed objects
	// (https://objects.example.invalid); PresignGet returns
	// {PublicBaseURL}/{key} when set.
	PublicBaseURL string
}

// S3 is a Store backed by any S3-compatible service (Garage, local cloud, …).
type S3 struct {
	client        *minio.Client
	bucket        string
	publicBaseURL string
}

var _ Store = (*S3)(nil)

// NewS3 builds an S3 client for cfg. It does not create the bucket.
func NewS3(cfg Config) (*S3, error) {
	endpoint, secure, err := parseEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("objectstore: bucket is required")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("objectstore: access key and secret key are required")
	}
	public, err := parsePublicBase(cfg.PublicBaseURL)
	if err != nil {
		return nil, err
	}
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: secure,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("objectstore: %w", err)
	}
	return &S3{client: client, bucket: cfg.Bucket, publicBaseURL: public}, nil
}

func parseEndpoint(raw string) (host string, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("objectstore: endpoint is required")
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "", false, fmt.Errorf("objectstore: endpoint must be host:port or an absolute http(s) URL without credentials")
		}
		return u.Host, u.Scheme == "https", nil
	}
	if strings.ContainsAny(raw, "/?#") {
		return "", false, fmt.Errorf("objectstore: endpoint must be host:port or an absolute http(s) URL without credentials")
	}
	return raw, false, nil
}

func parsePublicBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("objectstore: public base URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("objectstore: public base URL must be an absolute http(s) URL without credentials")
	}
	return strings.TrimRight(raw, "/"), nil
}

// PresignPut implements Store with Content-Type and Content-Length conditions.
func (s *S3) PresignPut(ctx context.Context, key, contentType string, contentLength int64, expires time.Duration) (string, error) {
	if key == "" || contentType == "" || contentLength < 1 || expires <= 0 {
		return "", fmt.Errorf("objectstore: invalid presign put arguments")
	}
	headers := http.Header{}
	headers.Set("Content-Type", contentType)
	headers.Set("Content-Length", strconv.FormatInt(contentLength, 10))
	u, err := s.client.PresignHeader(ctx, http.MethodPut, s.bucket, key, expires, nil, headers)
	if err != nil {
		return "", fmt.Errorf("objectstore: presign put: %w", err)
	}
	return u.String(), nil
}

// PresignGet implements Store. With a public base URL the object is addressed
// there (processed photos); otherwise a temporary signed GET is returned.
func (s *S3) PresignGet(ctx context.Context, key string, expires time.Duration) (string, error) {
	if key == "" {
		return "", fmt.Errorf("objectstore: key is required")
	}
	if s.publicBaseURL != "" {
		return s.publicBaseURL + "/" + strings.TrimLeft(key, "/"), nil
	}
	if expires <= 0 {
		return "", fmt.Errorf("objectstore: expires must be positive")
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, expires, nil)
	if err != nil {
		return "", fmt.Errorf("objectstore: presign get: %w", err)
	}
	return u.String(), nil
}

// Get implements Store.
func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("objectstore: get: %w", err)
	}
	defer func() { _ = obj.Close() }()
	b, err := io.ReadAll(obj)
	if err != nil {
		return nil, fmt.Errorf("objectstore: get: %w", err)
	}
	return b, nil
}

// Put implements Store.
func (s *S3) Put(ctx context.Context, key string, body []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("objectstore: put: %w", err)
	}
	return nil
}

// Delete implements Store. A missing object is not an error.
func (s *S3) Delete(ctx context.Context, key string) error {
	err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
	if err != nil {
		resp := minio.ToErrorResponse(err)
		if resp.Code == "NoSuchKey" || resp.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("objectstore: delete: %w", err)
	}
	return nil
}
