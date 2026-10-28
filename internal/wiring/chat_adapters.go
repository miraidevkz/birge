package wiring

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	chatmodel "birge/internal/domains/chat/model"
	chatport "birge/internal/domains/chat/port"
	membershipmodel "birge/internal/domains/membership/model"
	membershipservice "birge/internal/domains/membership/service"
	safetyservice "birge/internal/domains/safety/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/session"
)

// ChatMembers implements chat's MemberInfo from membership (first name only).
type ChatMembers struct {
	Members MemberLookup
}

var _ chatport.MemberInfo = (*ChatMembers)(nil)

// Profiles returns verification, first name, language and district. Unknown
// members are absent. Results are never logged.
func (d *ChatMembers) Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]chatmodel.Profile, error) {
	out := make(map[uuid.UUID]chatmodel.Profile, len(memberIDs))
	for _, id := range memberIDs {
		if id == uuid.Nil {
			continue
		}
		m, err := d.Members.Member(ctx, id)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("chat members: %w", err)
		}
		out[id] = chatmodel.Profile{
			Verified:   m.Verification == membershipmodel.VerificationVerified,
			FirstName:  firstName(m.DisplayName),
			Lang:       string(m.Lang),
			DistrictID: m.DistrictID,
		}
	}
	return out, nil
}

// ChatMentors implements chat's Mentors from the membership role "mentor".
type ChatMentors struct {
	Members MemberLookup
}

var _ chatport.Mentors = (*ChatMentors)(nil)

// IsMentor reports whether the member currently holds the mentor role.
func (m *ChatMentors) IsMentor(ctx context.Context, memberID uuid.UUID) (bool, error) {
	if memberID == uuid.Nil {
		return false, nil
	}
	mem, err := m.Members.Member(ctx, memberID)
	if errors.Is(err, membershipmodel.ErrMemberNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("chat mentors: %w", err)
	}
	return slices.Contains(mem.Roles, chatmodel.MentorRole), nil
}

// ChatCrisis adapts safety Detect / CrisisPhones / OpenEscalation to chat.
type ChatCrisis struct {
	Safety *safetyservice.Service
}

var _ chatport.Crisis = (*ChatCrisis)(nil)

// Detect runs the level-1 lexicon (local, never fails for missing hits).
// Running without it is a crisis path failure and is counted.
func (c *ChatCrisis) Detect(text, lang string) bool {
	if c == nil || c.Safety == nil {
		crisisPathError("lexicon_unavailable", errLexiconUnavailable)
		return false
	}
	return c.Safety.Detect(text, lang).Crisis
}

// CrisisPhones returns the configured crisis lines.
func (c *ChatCrisis) CrisisPhones() []chatmodel.CrisisPhone {
	if c == nil || c.Safety == nil {
		return nil
	}
	phones := c.Safety.CrisisPhones()
	out := make([]chatmodel.CrisisPhone, 0, len(phones))
	for _, p := range phones {
		out = append(out, chatmodel.CrisisPhone{Number: p.Number, Label: p.Label})
	}
	return out
}

// OpenEscalation opens (or returns the open) escalation with source "chat".
func (c *ChatCrisis) OpenEscalation(ctx context.Context, memberID uuid.UUID, source string) (uuid.UUID, error) {
	if c == nil || c.Safety == nil {
		err := errors.New("wiring: chat crisis not configured")
		crisisPathError("escalation_open", err)
		return uuid.Nil, err
	}
	id, _, err := c.Safety.OpenEscalation(ctx, memberID, source)
	crisisPathError("escalation_open", err)
	return id, err
}

// ChatAuthenticator validates the WebSocket auth-frame token with the member
// session middleware and resolves MemberID (creating the member on first use).
type ChatAuthenticator struct {
	Sessions *session.Middleware
	Members  *membershipservice.Service
}

var _ chatport.Authenticator = (*ChatAuthenticator)(nil)

// Authenticate validates token and returns a principal with MemberID set.
func (a *ChatAuthenticator) Authenticate(ctx context.Context, token string) (auth.Principal, error) {
	if a == nil || a.Sessions == nil || a.Members == nil {
		return auth.Principal{}, errors.New("wiring: chat authenticator not configured")
	}
	p, err := a.Sessions.Authenticate(ctx, token)
	if err != nil {
		return auth.Principal{}, err
	}
	m, err := a.Members.Me(auth.WithPrincipal(ctx, p))
	if err != nil {
		return auth.Principal{}, err
	}
	p.MemberID = m.ID
	return p, nil
}

// EmergencyBlocks also satisfies chat's Blocks port (same signature).
var _ chatport.Blocks = (*EmergencyBlocks)(nil)
