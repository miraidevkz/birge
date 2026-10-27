package objectstore

import (
	"context"
	"time"
)

// Disabled is a Store that fails every call with ErrNotConfigured. Used when
// BIRGE_S3_* is unset so the API still starts and only photo uploads fail.
type Disabled struct{}

var _ Store = Disabled{}

// PresignPut implements Store.
func (Disabled) PresignPut(context.Context, string, string, int64, time.Duration) (string, error) {
	return "", ErrNotConfigured
}

// PresignGet implements Store.
func (Disabled) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", ErrNotConfigured
}

// Get implements Store.
func (Disabled) Get(context.Context, string) ([]byte, error) {
	return nil, ErrNotConfigured
}

// Put implements Store.
func (Disabled) Put(context.Context, string, []byte, string) error {
	return ErrNotConfigured
}

// Delete implements Store.
func (Disabled) Delete(context.Context, string) error {
	return ErrNotConfigured
}
