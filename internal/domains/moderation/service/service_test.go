package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/moderation/model"
	"birge/internal/platform/auth"
)

// The store is nil: a refused call must never reach it.
func TestAuthorizationDeniesByDefault(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	id := uuid.New()
	cases := map[string]struct {
		p    auth.Principal
		want error
	}{
		"anonymous":        {auth.Principal{}, model.ErrUnauthorized},
		"member":           {auth.Principal{IdentityID: id, MemberID: uuid.New()}, model.ErrUnauthorized},
		"staff without id": {auth.Principal{Staff: true, Roles: []string{"admin"}, MFA: true}, model.ErrUnauthorized},
		"psychologist":     {auth.Principal{IdentityID: id, Staff: true, Roles: []string{"psychologist"}, MFA: true}, model.ErrForbidden},
		"lawyer":           {auth.Principal{IdentityID: id, Staff: true, Roles: []string{"lawyer"}, MFA: true}, model.ErrForbidden},
		"moderator no mfa": {auth.Principal{IdentityID: id, Staff: true, Roles: []string{"moderator"}}, model.ErrForbidden},
	}
	for name, c := range cases {
		if _, err := s.ListQueue(ctx, c.p, "", ""); !errors.Is(err, c.want) {
			t.Errorf("%s ListQueue: got %v, want %v", name, err, c.want)
		}
		if err := s.Decide(ctx, c.p, uuid.New(), model.Decision{Approve: true}); !errors.Is(err, c.want) {
			t.Errorf("%s Decide: got %v, want %v", name, err, c.want)
		}
	}
}

func TestCursorRoundTripAndRejectsGarbage(t *testing.T) {
	at := time.Date(2026, 10, 27, 12, 0, 0, 123456789, time.UTC)
	id := uuid.New()
	gotAt, gotID, err := decodeCursor(encodeCursor(at, id))
	if err != nil || !gotAt.Equal(at) || gotID != id {
		t.Fatalf("round trip: %v %v %v", gotAt, gotID, err)
	}
	for _, c := range []string{"!!", "bm90LWEtY3Vyc29y", encodeCursor(at, id) + "x"} {
		if _, _, err := decodeCursor(c); !errors.Is(err, model.ErrInvalidInput) {
			t.Errorf("decodeCursor(%q): got %v", c, err)
		}
	}
}

func TestHandleEventRejectsMalformedAndIgnoresUnknown(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	if err := s.HandleEvent(ctx, uuid.New(), "safety.EscalationOpened", []byte(`{}`)); err != nil {
		t.Fatalf("unknown type: %v", err)
	}
	for typ, data := range map[string]string{
		model.EventListingSubmitted:      `{"listing_id":"00000000-0000-0000-0000-000000000000"}`,
		model.EventItemSubmitted:         `{"object_type":"listing","object_id":"` + uuid.NewString() + `"}`,
		model.EventMessageHeld:           `not json`,
		model.EventVerificationRequested: `{"member_id":"` + uuid.NewString() + `"}`,
	} {
		if err := s.HandleEvent(ctx, uuid.New(), typ, []byte(data)); !errors.Is(err, model.ErrInvalidInput) {
			t.Errorf("%s: got %v, want ErrInvalidInput", typ, err)
		}
	}
}
