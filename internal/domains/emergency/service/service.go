package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
)

const defaultAutoCloseAfter = 24 * time.Hour

// ErrNotFound is returned to other contexts for unknown, removed or
// unconfirmed trusted contacts (re-exported from model, which other contexts
// cannot import).
var ErrNotFound = model.ErrNotFound

// Settings is the emergency runtime configuration.
type Settings struct {
	// AutoCloseAfter is how long an SOS stays open before it closes by itself
	// (default 24 hours, § 4.6 "конфиг").
	AutoCloseAfter time.Duration
}

// Service is the emergency application service.
type Service struct {
	Store         port.Store
	Sealer        port.Sealer
	Members       port.MemberInfo
	Blocks        port.Blocks
	Acquaintances port.Acquaintances
	Clock         clock.Clock
	Logger        *slog.Logger
	Settings      Settings
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) autoCloseAfter() time.Duration {
	if s.Settings.AutoCloseAfter > 0 {
		return s.Settings.AutoCloseAfter
	}
	return defaultAutoCloseAfter
}

// memberOf returns the caller's member id. Data is keyed by the membership
// member id (auth.Principal.MemberID), never by the identity provider's id;
// staff and anonymous sessions are refused.
func memberOf(p auth.Principal) (uuid.UUID, error) {
	if p.Staff || p.MemberID == uuid.Nil {
		return uuid.Nil, model.ErrUnauthorized
	}
	return p.MemberID, nil
}

func (s *Service) seal(ctx context.Context, owner uuid.UUID, plaintext, field string, rowID uuid.UUID) ([]byte, error) {
	if s.Sealer == nil {
		return nil, errors.New("emergency: sealer not configured")
	}
	b, err := s.Sealer.Seal(ctx, owner, []byte(plaintext), crypto.AssociatedData(field, rowID))
	if err != nil {
		return nil, fmt.Errorf("emergency: seal %s: %w", field, err)
	}
	return b, nil
}

func (s *Service) open(ctx context.Context, owner uuid.UUID, sealed []byte, field string, rowID uuid.UUID) (string, error) {
	if s.Sealer == nil {
		return "", errors.New("emergency: sealer not configured")
	}
	b, err := s.Sealer.Open(ctx, owner, sealed, crypto.AssociatedData(field, rowID))
	if err != nil {
		return "", fmt.Errorf("emergency: open %s: %w", field, err)
	}
	return string(b), nil
}

// profiles reads member profiles; unknown members are absent.
func (s *Service) profiles(ctx context.Context, ids ...uuid.UUID) (map[uuid.UUID]model.Profile, error) {
	if s.Members == nil {
		return nil, errors.New("emergency: member info not configured")
	}
	if len(ids) == 0 {
		return map[uuid.UUID]model.Profile{}, nil
	}
	m, err := s.Members.Profiles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("emergency: member profiles: %w", err)
	}
	return m, nil
}

// profile reads one member's profile, or model.ErrNotFound.
func (s *Service) profile(ctx context.Context, id uuid.UUID) (model.Profile, error) {
	m, err := s.profiles(ctx, id)
	if err != nil {
		return model.Profile{}, err
	}
	p, ok := m[id]
	if !ok {
		return model.Profile{}, model.ErrNotFound
	}
	return p, nil
}

// blocked reports whether a and b blocked each other in either direction.
// It fails closed: an error is returned, never "not blocked".
func (s *Service) blocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if s.Blocks == nil {
		return false, errors.New("emergency: blocks not configured")
	}
	v, err := s.Blocks.IsBlocked(ctx, a, b)
	if err != nil {
		return false, fmt.Errorf("emergency: blocks: %w", err)
	}
	return v, nil
}

// met reports whether a and b confirmed they met in person.
func (s *Service) met(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if s.Acquaintances == nil {
		return false, errors.New("emergency: acquaintances not configured")
	}
	v, err := s.Acquaintances.Met(ctx, a, b)
	if err != nil {
		return false, fmt.Errorf("emergency: acquaintances: %w", err)
	}
	return v, nil
}

// encodeCursor makes an opaque keyset cursor from the last row of a page.
func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + "." + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	ns, idStr, ok := strings.Cut(string(raw), ".")
	if !ok {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	return time.Unix(0, n).UTC(), id, nil
}
