package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/chat/model"
	"birge/internal/domains/chat/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/crypto"
)

// Defaults of Settings (ADR-0016 and the brief; country configuration may
// change them).
const (
	DefaultRetentionGroup  = 365 * 24 * time.Hour
	DefaultRetentionDirect = 90 * 24 * time.Hour
	DefaultRetentionAt     = 3 * time.Hour // 03:00 UTC
	DefaultNewcomerHeld    = 3
	DefaultRateLimit       = 20
	DefaultRateWindow      = time.Minute
	DefaultPushInterval    = 10 * time.Minute
	DefaultPresenceTTL     = 90 * time.Second

	// profileTimeout bounds the best-effort language lookup on send, so a
	// slow membership context cannot delay the crisis check.
	profileTimeout = 2 * time.Second
	// crisisRetryAfter is when the escalation timer of a crisis message
	// fires: after the synchronous attempt normally succeeded and cancelled
	// it, soon enough for the 15-minute backup (ADR-0023).
	crisisRetryAfter = 30 * time.Second
	// purgePage is the retention batch size.
	purgePage = 1000
	// renderBatch is the raw page size when filtering history for a reader.
	renderBatch = 100
)

// ErrNotFound is returned to other contexts and the gateway for unknown
// rooms and messages (re-exported from model, which other contexts cannot import).
var ErrNotFound = model.ErrNotFound

// Settings is the chat runtime configuration. Zero values take the defaults.
type Settings struct {
	// RetentionGroup and RetentionDirect are how long message texts are kept
	// in group and direct rooms (ADR-0016: 1 year, 90 days).
	RetentionGroup  time.Duration
	RetentionDirect time.Duration
	// RetentionAt is the time of day (UTC, offset from midnight) of the daily sweep.
	RetentionAt time.Duration
	// NewcomerHeld is how many of a member's first group-room messages are
	// held for moderation. Negative disables the newcomer hold.
	NewcomerHeld int
	// RateLimit messages per RateWindow per member, across all rooms.
	RateLimit  int
	RateWindow time.Duration
	// PushInterval is the least time between two chat.new_message pushes
	// for one room to one member.
	PushInterval time.Duration
	// PresenceTTL is how long a gateway heartbeat counts as "connected to the room".
	PresenceTTL time.Duration
}

// Service is the chat application service.
type Service struct {
	Store   port.Store
	Sealer  port.Sealer
	Members port.MemberInfo
	Blocks  port.Blocks
	Mentors port.Mentors
	// Crisis is required: without it Send fails rather than skipping the
	// crisis check (AGENTS.md non-negotiable 1).
	Crisis   port.Crisis
	Fanout   port.Fanout
	Clock    clock.Clock
	Logger   *slog.Logger
	Settings Settings
}

var (
	errReplay    = errors.New("chat: replayed client id")
	errDuplicate = errors.New("chat: concurrent duplicate client id")
)

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func orDefault[T int | time.Duration](v, d T) T {
	if v > 0 {
		return v
	}
	return d
}

func (s *Service) retentionGroup() time.Duration {
	return orDefault(s.Settings.RetentionGroup, DefaultRetentionGroup)
}

func (s *Service) retentionDirect() time.Duration {
	return orDefault(s.Settings.RetentionDirect, DefaultRetentionDirect)
}

func (s *Service) retentionAt() time.Duration {
	if s.Settings.RetentionAt > 0 && s.Settings.RetentionAt < 24*time.Hour {
		return s.Settings.RetentionAt
	}
	return DefaultRetentionAt
}

func (s *Service) newcomerHeld() int {
	if s.Settings.NewcomerHeld < 0 {
		return 0
	}
	return orDefault(s.Settings.NewcomerHeld, DefaultNewcomerHeld)
}

func (s *Service) rateLimit() int { return orDefault(s.Settings.RateLimit, DefaultRateLimit) }

func (s *Service) rateWindow() time.Duration {
	return orDefault(s.Settings.RateWindow, DefaultRateWindow)
}

func (s *Service) pushInterval() time.Duration {
	return orDefault(s.Settings.PushInterval, DefaultPushInterval)
}

func (s *Service) presenceTTL() time.Duration {
	return orDefault(s.Settings.PresenceTTL, DefaultPresenceTTL)
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

func (s *Service) seal(ctx context.Context, author uuid.UUID, text string, messageID uuid.UUID) ([]byte, error) {
	if s.Sealer == nil {
		return nil, errors.New("chat: sealer not configured")
	}
	b, err := s.Sealer.Seal(ctx, author, []byte(text), crypto.AssociatedData(model.FieldText, messageID))
	if err != nil {
		return nil, fmt.Errorf("chat: seal message: %w", err)
	}
	return b, nil
}

// open opens a message text. A purged text, a shredded author key or a
// ciphertext that no longer opens yields deleted=true, not an error: the
// reader sees "message deleted" (ADR-0016).
func (s *Service) open(ctx context.Context, m model.Message) (text string, deleted bool, err error) {
	if m.Text == nil || m.PurgedAt != nil {
		return "", true, nil
	}
	if s.Sealer == nil {
		return "", false, errors.New("chat: sealer not configured")
	}
	b, err := s.Sealer.Open(ctx, m.AuthorID, m.Text, crypto.AssociatedData(model.FieldText, m.ID))
	if errors.Is(err, crypto.ErrShredded) || errors.Is(err, crypto.ErrInvalidCiphertext) {
		return "", true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("chat: open message: %w", err)
	}
	return string(b), false, nil
}

func (s *Service) profiles(ctx context.Context, ids ...uuid.UUID) (map[uuid.UUID]model.Profile, error) {
	if s.Members == nil {
		return nil, errors.New("chat: member info not configured")
	}
	if len(ids) == 0 {
		return map[uuid.UUID]model.Profile{}, nil
	}
	m, err := s.Members.Profiles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("chat: member profiles: %w", err)
	}
	return m, nil
}

// profileBestEffort reads one profile with a short timeout and returns the
// zero profile on any failure. Used where the profile is optional (the
// language order of the crisis check, the author's own name in an ack).
func (s *Service) profileBestEffort(ctx context.Context, id uuid.UUID) model.Profile {
	ctx, cancel := context.WithTimeout(ctx, profileTimeout)
	defer cancel()
	m, err := s.profiles(ctx, id)
	if err != nil {
		s.log().Warn("chat profile lookup failed", "event", "ChatProfileUnavailable", "error", err)
		return model.Profile{}
	}
	return m[id]
}

// blocked reports whether a and b blocked each other in either direction.
// It fails closed: an error is returned, never "not blocked".
func (s *Service) blocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if a == b {
		return false, nil
	}
	if s.Blocks == nil {
		return false, errors.New("chat: blocks not configured")
	}
	v, err := s.Blocks.IsBlocked(ctx, a, b)
	if err != nil {
		return false, fmt.Errorf("chat: blocks: %w", err)
	}
	return v, nil
}

// notify publishes a fan-out notice after commit. Failures are logged, not
// returned: the change is committed and clients recover by sequence.
func (s *Service) notify(ctx context.Context, n model.Notice) {
	if s.Fanout == nil {
		return
	}
	if err := s.Fanout.Publish(ctx, n); err != nil {
		s.log().Warn("chat fan-out publish failed", "event", "ChatFanoutFailed", "room_id", n.RoomID, "error", err)
	}
}

// activeMember checks that member is in the room and returns the room.
func activeMember(ctx context.Context, tx port.Tx, roomID, member uuid.UUID) (model.Room, error) {
	room, err := tx.GetRoom(ctx, roomID, false)
	if err != nil {
		return model.Room{}, err
	}
	m, err := tx.GetRoomMember(ctx, roomID, member)
	if errors.Is(err, model.ErrNotFound) || (err == nil && !m.Active()) {
		return model.Room{}, model.ErrNotMember
	}
	if err != nil {
		return model.Room{}, err
	}
	return room, nil
}
