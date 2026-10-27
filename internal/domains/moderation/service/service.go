package service

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
	"birge/internal/domains/moderation/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
)

// Service is the moderation application service.
type Service struct {
	Store port.Store
	// Sources holds the content source of each kind. A kind without one
	// cannot be listed (the page fails); decisions do not need it.
	Sources map[model.Kind]port.ContentSource
	Clock   clock.Clock
	Logger  *slog.Logger
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

// staff authorizes a moderation call and returns the role the caller acts
// in. Deny by default: members, anonymous callers, staff without MFA or
// without the moderator or admin role are refused.
func staff(p auth.Principal) (string, error) {
	if !p.Staff || p.IdentityID == uuid.Nil {
		return "", model.ErrUnauthorized
	}
	role := model.StaffRole(p.Roles)
	if role == "" || !p.MFA {
		return "", model.ErrForbidden
	}
	return role, nil
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + "." + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	bad := fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	if len(c) > model.MaxCursorLen {
		return time.Time{}, uuid.Nil, bad
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, bad
	}
	ns, idStr, ok := strings.Cut(string(raw), ".")
	if !ok {
		return time.Time{}, uuid.Nil, bad
	}
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, bad
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, bad
	}
	return time.Unix(0, n).UTC(), id, nil
}
