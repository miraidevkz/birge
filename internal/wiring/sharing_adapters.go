package wiring

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	directorymodel "birge/internal/domains/directory/model"
	directoryport "birge/internal/domains/directory/port"
	membershipmodel "birge/internal/domains/membership/model"
	sharingmodel "birge/internal/domains/sharing/model"
	sharingport "birge/internal/domains/sharing/port"
)

// SharingMembers implements sharing's MemberInfo (verification only).
type SharingMembers struct {
	Members MemberLookup
}

var _ sharingport.MemberInfo = (*SharingMembers)(nil)

// Profiles returns verification for each id. Unknown members are absent.
func (d *SharingMembers) Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]sharingmodel.Profile, error) {
	out := make(map[uuid.UUID]sharingmodel.Profile, len(memberIDs))
	for _, id := range memberIDs {
		if id == uuid.Nil {
			continue
		}
		m, err := d.Members.Member(ctx, id)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("sharing members: %w", err)
		}
		out[id] = sharingmodel.Profile{
			Verified: m.Verification == membershipmodel.VerificationVerified,
		}
	}
	return out, nil
}

// DirectoryMembers implements directory's MemberInfo (verification + first name).
type DirectoryMembers struct {
	Members MemberLookup
}

var _ directoryport.MemberInfo = (*DirectoryMembers)(nil)

// Profiles returns verification and first-name-only display names.
func (d *DirectoryMembers) Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]directorymodel.Profile, error) {
	out := make(map[uuid.UUID]directorymodel.Profile, len(memberIDs))
	for _, id := range memberIDs {
		if id == uuid.Nil {
			continue
		}
		m, err := d.Members.Member(ctx, id)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("directory members: %w", err)
		}
		out[id] = directorymodel.Profile{
			Verified:    m.Verification == membershipmodel.VerificationVerified,
			DisplayName: firstName(m.DisplayName),
		}
	}
	return out, nil
}

// EmergencyBlocks also satisfies sharing's Blocks port.
var _ sharingport.Blocks = (*EmergencyBlocks)(nil)
