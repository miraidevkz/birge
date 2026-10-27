package wiring

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	circlemodel "birge/internal/domains/circle/model"
	circleport "birge/internal/domains/circle/port"
	circleservice "birge/internal/domains/circle/service"
	emergencyport "birge/internal/domains/emergency/port"
	membershipmodel "birge/internal/domains/membership/model"
)

// CircleMembers implements circle's MemberInfo from membership.
type CircleMembers struct {
	Members MemberLookup
}

var _ circleport.MemberInfo = (*CircleMembers)(nil)

// Profiles returns verification, district, language, first name and children.
// Unknown members are absent. Results are never logged.
func (d *CircleMembers) Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]circlemodel.Profile, error) {
	out := make(map[uuid.UUID]circlemodel.Profile, len(memberIDs))
	for _, id := range memberIDs {
		if id == uuid.Nil {
			continue
		}
		m, err := d.Members.Member(ctx, id)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("circle members: %w", err)
		}
		children := make([]circlemodel.Child, 0, len(m.Children))
		for _, c := range m.Children {
			children = append(children, circlemodel.Child{
				BirthYear: c.BirthYear,
				Needs:     append([]string(nil), c.Needs...),
			})
		}
		out[id] = circlemodel.Profile{
			Verified:   m.Verification == membershipmodel.VerificationVerified,
			DistrictID: m.DistrictID,
			Lang:       string(m.Lang),
			FirstName:  firstName(m.DisplayName),
			Children:   children,
		}
	}
	return out, nil
}

// CircleMet adapts circle.Service.Met to emergency's Acquaintances port so
// member trusted contacts work after both confirmed they met in person.
type CircleMet struct {
	Circle *circleservice.Service
}

var _ emergencyport.Acquaintances = (*CircleMet)(nil)

// Met reports whether a and b both confirmed they met in person.
func (c *CircleMet) Met(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if c == nil || c.Circle == nil {
		return false, nil
	}
	return c.Circle.Met(ctx, a, b)
}

// EmergencyBlocks also satisfies circle's Blocks port (same signature).
var _ circleport.Blocks = (*EmergencyBlocks)(nil)
