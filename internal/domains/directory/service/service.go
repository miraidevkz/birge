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
	"unicode/utf8"

	"github.com/google/uuid"

	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
)

// Service is the directory application service.
type Service struct {
	Store   port.Store
	Members port.MemberInfo
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

func memberOf(p auth.Principal) (uuid.UUID, error) {
	if p.Staff || p.MemberID == uuid.Nil {
		return uuid.Nil, model.ErrUnauthorized
	}
	return p.MemberID, nil
}

func (s *Service) profiles(ctx context.Context, ids ...uuid.UUID) (map[uuid.UUID]model.Profile, error) {
	if s.Members == nil {
		return nil, errors.New("directory: member info not configured")
	}
	if len(ids) == 0 {
		return map[uuid.UUID]model.Profile{}, nil
	}
	m, err := s.Members.Profiles(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("directory: member profiles: %w", err)
	}
	return m, nil
}

func (s *Service) requireVerified(ctx context.Context, memberID uuid.UUID) error {
	m, err := s.profiles(ctx, memberID)
	if err != nil {
		return err
	}
	p, ok := m[memberID]
	if !ok || !p.Verified {
		return model.ErrForbidden
	}
	return nil
}

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

func validateSpecialist(in model.NewSpecialist) error {
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < model.MinNameLen || n > model.MaxNameLen {
		return fmt.Errorf("%w: name", model.ErrInvalidInput)
	}
	if !model.CodeIsValid(in.Profession) {
		return fmt.Errorf("%w: profession", model.ErrInvalidInput)
	}
	if in.DistrictID == uuid.Nil {
		return fmt.Errorf("%w: districtId", model.ErrInvalidInput)
	}
	if len(in.Needs) > model.MaxNeeds {
		return fmt.Errorf("%w: needs", model.ErrInvalidInput)
	}
	for _, n := range in.Needs {
		if !model.CodeIsValid(n) {
			return fmt.Errorf("%w: needs", model.ErrInvalidInput)
		}
	}
	if in.Contacts != nil {
		c := strings.TrimSpace(*in.Contacts)
		if utf8.RuneCountInString(c) > model.MaxContactsLen {
			return fmt.Errorf("%w: contacts", model.ErrInvalidInput)
		}
	}
	return nil
}

func validateReview(in model.NewReview) error {
	if in.Rating < 1 || in.Rating > 5 {
		return fmt.Errorf("%w: rating", model.ErrInvalidInput)
	}
	text := strings.TrimSpace(in.Text)
	if n := utf8.RuneCountInString(text); n < 1 || n > model.MaxReviewTextLen {
		return fmt.Errorf("%w: text", model.ErrInvalidInput)
	}
	return nil
}

func firstName(display string) string {
	display = strings.TrimSpace(display)
	if display == "" {
		return ""
	}
	if i := strings.IndexAny(display, " \t"); i >= 0 {
		return display[:i]
	}
	return display
}
