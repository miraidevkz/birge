package wiring

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	chatmodel "birge/internal/domains/chat/model"
	chatport "birge/internal/domains/chat/port"
	chatservice "birge/internal/domains/chat/service"
	directorymodel "birge/internal/domains/directory/model"
	directoryservice "birge/internal/domains/directory/service"
	membershipmodel "birge/internal/domains/membership/model"
	membershipservice "birge/internal/domains/membership/service"
	moderationmodel "birge/internal/domains/moderation/model"
	moderationport "birge/internal/domains/moderation/port"
	sharingmodel "birge/internal/domains/sharing/model"
	sharingservice "birge/internal/domains/sharing/service"
)

// SharingContentSource adapts sharing.ModerationSummary to moderation.ContentSource.
type SharingContentSource struct {
	Sharing *sharingservice.Service
}

var _ moderationport.ContentSource = (*SharingContentSource)(nil)

// Summary returns listing title/description and photo URL; no contacts.
func (a *SharingContentSource) Summary(ctx context.Context, objectID uuid.UUID) (moderationmodel.Summary, error) {
	if a == nil || a.Sharing == nil {
		return moderationmodel.Summary{}, errors.New("wiring: sharing content source not configured")
	}
	text, photo, err := a.Sharing.ModerationSummary(ctx, objectID)
	if errors.Is(err, sharingmodel.ErrNotFound) {
		return moderationmodel.Summary{}, moderationmodel.ErrNotFound
	}
	if err != nil {
		return moderationmodel.Summary{}, fmt.Errorf("sharing summary: %w", err)
	}
	return moderationmodel.Summary{Text: text, PhotoURL: photo}, nil
}

// DirectorySpecialistSource adapts directory.SpecialistSummary.
type DirectorySpecialistSource struct {
	Directory *directoryservice.Service
}

var _ moderationport.ContentSource = (*DirectorySpecialistSource)(nil)

// Summary returns a specialist card without contacts.
func (a *DirectorySpecialistSource) Summary(ctx context.Context, objectID uuid.UUID) (moderationmodel.Summary, error) {
	if a == nil || a.Directory == nil {
		return moderationmodel.Summary{}, errors.New("wiring: directory specialist source not configured")
	}
	text, err := a.Directory.SpecialistSummary(ctx, objectID)
	if errors.Is(err, directorymodel.ErrNotFound) {
		return moderationmodel.Summary{}, moderationmodel.ErrNotFound
	}
	if err != nil {
		return moderationmodel.Summary{}, fmt.Errorf("specialist summary: %w", err)
	}
	return moderationmodel.Summary{Text: text}, nil
}

// DirectoryReviewSource adapts directory.ReviewSummary.
type DirectoryReviewSource struct {
	Directory *directoryservice.Service
}

var _ moderationport.ContentSource = (*DirectoryReviewSource)(nil)

// Summary returns review text without contacts.
func (a *DirectoryReviewSource) Summary(ctx context.Context, objectID uuid.UUID) (moderationmodel.Summary, error) {
	if a == nil || a.Directory == nil {
		return moderationmodel.Summary{}, errors.New("wiring: directory review source not configured")
	}
	text, err := a.Directory.ReviewSummary(ctx, objectID)
	if errors.Is(err, directorymodel.ErrNotFound) {
		return moderationmodel.Summary{}, moderationmodel.ErrNotFound
	}
	if err != nil {
		return moderationmodel.Summary{}, fmt.Errorf("review summary: %w", err)
	}
	return moderationmodel.Summary{Text: text}, nil
}

// ChatContentSource adapts chat.ModerationSummary for report and chat_message.
type ChatContentSource struct {
	Chat *chatservice.Service
}

var _ moderationport.ContentSource = (*ChatContentSource)(nil)

// Summary returns opened message text; purged/gone → ErrNotFound.
func (a *ChatContentSource) Summary(ctx context.Context, objectID uuid.UUID) (moderationmodel.Summary, error) {
	if a == nil || a.Chat == nil {
		return moderationmodel.Summary{}, errors.New("wiring: chat content source not configured")
	}
	text, err := a.Chat.ModerationSummary(ctx, objectID)
	if errors.Is(err, chatmodel.ErrNotFound) {
		return moderationmodel.Summary{}, moderationmodel.ErrNotFound
	}
	if err != nil {
		return moderationmodel.Summary{}, fmt.Errorf("chat summary: %w", err)
	}
	return moderationmodel.Summary{Text: text}, nil
}

// MembershipVerificationSource adapts membership.VerificationSummary.
type MembershipVerificationSource struct {
	Members *membershipservice.Service
}

var _ moderationport.ContentSource = (*MembershipVerificationSource)(nil)

// Summary returns first name and district only.
func (a *MembershipVerificationSource) Summary(ctx context.Context, objectID uuid.UUID) (moderationmodel.Summary, error) {
	if a == nil || a.Members == nil {
		return moderationmodel.Summary{}, errors.New("wiring: membership verification source not configured")
	}
	text, err := a.Members.VerificationSummary(ctx, objectID)
	if errors.Is(err, membershipmodel.ErrMemberNotFound) {
		return moderationmodel.Summary{}, moderationmodel.ErrNotFound
	}
	if err != nil {
		return moderationmodel.Summary{}, fmt.Errorf("verification summary: %w", err)
	}
	return moderationmodel.Summary{Text: text}, nil
}

// ChatAuthorSuspender adapts membership.SuspendAuthor to chat's AuthorSuspender.
type ChatAuthorSuspender struct {
	Members *membershipservice.Service
}

var _ chatport.AuthorSuspender = (*ChatAuthorSuspender)(nil)

// SuspendAuthor suspends the message author after block_author.
func (a *ChatAuthorSuspender) SuspendAuthor(ctx context.Context, memberID uuid.UUID, reason string, moderationItemID uuid.UUID) error {
	if a == nil || a.Members == nil {
		return errors.New("wiring: author suspender not configured")
	}
	return a.Members.SuspendAuthor(ctx, memberID, reason, moderationItemID)
}

// moderationSources builds the ContentSource map for every moderation kind.
func moderationSources(
	chat *chatservice.Service,
	sharing *sharingservice.Service,
	directory *directoryservice.Service,
	members *membershipservice.Service,
) map[moderationmodel.Kind]moderationport.ContentSource {
	chatSrc := &ChatContentSource{Chat: chat}
	return map[moderationmodel.Kind]moderationport.ContentSource{
		moderationmodel.KindListing:      &SharingContentSource{Sharing: sharing},
		moderationmodel.KindSpecialist:   &DirectorySpecialistSource{Directory: directory},
		moderationmodel.KindReview:       &DirectoryReviewSource{Directory: directory},
		moderationmodel.KindReport:       chatSrc,
		moderationmodel.KindChatMessage:  chatSrc,
		moderationmodel.KindVerification: &MembershipVerificationSource{Members: members},
	}
}
