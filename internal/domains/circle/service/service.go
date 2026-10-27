package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/circle/model"
	"birge/internal/domains/circle/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
)

// Settings is the circle runtime configuration (§ 4.4 «конфиг»).
type Settings struct {
	// RequestTTL is how long an open tea request stays open (default 7 days).
	RequestTTL time.Duration
	// MatchInterval is how often circle.MatchRun runs (default 1 hour).
	MatchInterval time.Duration
	// AskMetAfter is the delay after mutual accept before asking "did you meet?"
	// (default 3 days).
	AskMetAfter time.Duration
	// RematchCooldown is how long a pair cannot be matched again (default 30 days).
	RematchCooldown time.Duration
	// ScoreWeights are the matching bonuses (district, age, need, language).
	ScoreWeights model.ScoreWeights
}

// Service is the circle application service.
type Service struct {
	Store    port.Store
	Members  port.MemberInfo
	Blocks   port.Blocks
	Clock    clock.Clock
	Logger   *slog.Logger
	Settings Settings
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) requestTTL() time.Duration {
	if s.Settings.RequestTTL > 0 {
		return s.Settings.RequestTTL
	}
	return model.DefaultRequestTTL
}

func (s *Service) matchInterval() time.Duration {
	if s.Settings.MatchInterval > 0 {
		return s.Settings.MatchInterval
	}
	return model.DefaultMatchInterval
}

func (s *Service) askMetAfter() time.Duration {
	if s.Settings.AskMetAfter > 0 {
		return s.Settings.AskMetAfter
	}
	return model.DefaultAskMetAfter
}

func (s *Service) rematchCooldown() time.Duration {
	if s.Settings.RematchCooldown > 0 {
		return s.Settings.RematchCooldown
	}
	return model.DefaultRematchCooldown
}

func (s *Service) weights() model.ScoreWeights {
	return s.Settings.ScoreWeights.Normalize()
}

// memberOf returns the caller's member id. Data is keyed by MemberID, never
// IdentityID; staff and anonymous sessions are refused.
func memberOf(p auth.Principal) (uuid.UUID, error) {
	if p.Staff || p.MemberID == uuid.Nil {
		return uuid.Nil, model.ErrUnauthorized
	}
	return p.MemberID, nil
}

func (s *Service) profiles(ctx context.Context, ids ...uuid.UUID) (map[uuid.UUID]model.Profile, error) {
	if s.Members == nil {
		return nil, errors.New("circle: member info not configured")
	}
	if len(ids) == 0 {
		return map[uuid.UUID]model.Profile{}, nil
	}
	m, err := s.Members.Profiles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("circle: member profiles: %w", err)
	}
	return m, nil
}

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
		return false, errors.New("circle: blocks not configured")
	}
	v, err := s.Blocks.IsBlocked(ctx, a, b)
	if err != nil {
		return false, fmt.Errorf("circle: blocks: %w", err)
	}
	return v, nil
}

func ptrBool(v bool) *bool { return &v }

func boolVal(p *bool) (bool, bool) {
	if p == nil {
		return false, false
	}
	return *p, true
}
