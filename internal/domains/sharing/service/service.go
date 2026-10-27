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

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
)

// Settings is the sharing runtime configuration (§ 4.5 «конфиг»).
type Settings struct {
	// ListingTTL is how long an active listing stays open (default 30 days).
	ListingTTL time.Duration
}

// Service is the sharing application service.
type Service struct {
	Store    port.Store
	Members  port.MemberInfo
	Blocks   port.Blocks
	Objects  port.ObjectStore
	Images   port.ImageProcessor
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

func (s *Service) listingTTL() time.Duration {
	if s.Settings.ListingTTL > 0 {
		return s.Settings.ListingTTL
	}
	return model.DefaultListingTTL
}

// memberOf returns the caller's member id. Data is keyed by MemberID, never
// IdentityID; staff and anonymous sessions are refused.
func memberOf(p auth.Principal) (uuid.UUID, error) {
	if p.Staff || p.MemberID == uuid.Nil {
		return uuid.Nil, model.ErrUnauthorized
	}
	return p.MemberID, nil
}

func (s *Service) profile(ctx context.Context, id uuid.UUID) (model.Profile, error) {
	if s.Members == nil {
		return model.Profile{}, errors.New("sharing: member info not configured")
	}
	m, err := s.Members.Profiles(ctx, []uuid.UUID{id})
	if err != nil {
		return model.Profile{}, fmt.Errorf("sharing: member profiles: %w", err)
	}
	p, ok := m[id]
	if !ok {
		return model.Profile{}, model.ErrNotFound
	}
	return p, nil
}

func (s *Service) requireVerified(ctx context.Context, memberID uuid.UUID) error {
	prof, err := s.profile(ctx, memberID)
	if errors.Is(err, model.ErrNotFound) {
		return model.ErrForbidden
	}
	if err != nil {
		return err
	}
	if !prof.Verified {
		return model.ErrForbidden
	}
	return nil
}

// blocked reports whether a and b blocked each other in either direction.
// It fails closed: an error is returned, never "not blocked".
func (s *Service) blocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if s.Blocks == nil {
		return false, errors.New("sharing: blocks not configured")
	}
	v, err := s.Blocks.IsBlocked(ctx, a, b)
	if err != nil {
		return false, fmt.Errorf("sharing: blocks: %w", err)
	}
	return v, nil
}

func (s *Service) photoURL(ctx context.Context, key *string) (string, error) {
	if key == nil || *key == "" {
		return "", nil
	}
	if s.Objects == nil {
		return "", errors.New("sharing: object store not configured")
	}
	return s.Objects.PresignGet(ctx, *key, model.PhotoURLTTL)
}

func (s *Service) toView(ctx context.Context, l model.Listing, viewer uuid.UUID) (model.ListingView, error) {
	url, err := s.photoURL(ctx, l.PhotoObjectKey)
	if err != nil {
		return model.ListingView{}, err
	}
	return model.ListingView{Listing: l, PhotoURL: url, Mine: l.AuthorID == viewer}, nil
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

func validateNewListing(in model.NewListing) error {
	if !in.AcceptRules {
		return fmt.Errorf("%w: acceptRules", model.ErrInvalidInput)
	}
	if !model.TypeIsValid(in.Type) {
		return fmt.Errorf("%w: type", model.ErrInvalidInput)
	}
	if !model.CodeIsValid(in.Category) {
		return fmt.Errorf("%w: category", model.ErrInvalidInput)
	}
	title := strings.TrimSpace(in.Title)
	if n := utf8.RuneCountInString(title); n < model.MinTitleLen || n > model.MaxTitleLen {
		return fmt.Errorf("%w: title", model.ErrInvalidInput)
	}
	desc := strings.TrimSpace(in.Description)
	if n := utf8.RuneCountInString(desc); n < 1 || n > model.MaxDescriptionLen {
		return fmt.Errorf("%w: description", model.ErrInvalidInput)
	}
	if in.DistrictID == uuid.Nil {
		return fmt.Errorf("%w: districtId", model.ErrInvalidInput)
	}
	return nil
}

// tryActivate moves a pending, approved, photo-ready listing to active and
// schedules auto-close. Idempotent when already active.
func (s *Service) tryActivate(ctx context.Context, tx port.Tx, listing model.Listing, now time.Time) (model.Listing, error) {
	if listing.Status == model.ListingActive {
		return listing, nil
	}
	if listing.Status != model.ListingPending || listing.ApprovedAt == nil || !listing.PhotoReady() {
		return listing, nil
	}
	closesAt := now.Add(s.listingTTL())
	activated, updated, err := tx.ActivateListing(ctx, listing.ID, closesAt)
	if err != nil {
		return model.Listing{}, err
	}
	if !updated {
		return listing, nil
	}
	if _, err := tx.ScheduleAutoClose(ctx, activated.ID, closesAt); err != nil {
		return model.Listing{}, err
	}
	s.log().Info("listing activated", "event", "ListingActivated", "listing_id", activated.ID)
	return activated, nil
}
