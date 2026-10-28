package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/platform/outbox"
)

// StaffListMembers lists verified or suspended members for staff.
// Staff moderator or admin with MFA (deny by default). Every read is audited
// in the platform audit trail without personal data.
func (s *Service) StaffListMembers(ctx context.Context, f model.StaffMemberListFilter) (model.StaffMemberPage, error) {
	p, err := staffModerator(ctx)
	if err != nil {
		return model.StaffMemberPage{}, err
	}
	if f.Status != model.StaffMemberStatusVerified && f.Status != model.StaffMemberStatusSuspended {
		return model.StaffMemberPage{}, fmt.Errorf("%w: status must be verified or suspended", model.ErrInvalidRequest)
	}

	filter := port.StaffMemberListFilter{
		Status:     f.Status,
		DistrictID: f.DistrictID,
		Limit:      int32(model.StaffMemberPageSize + 1),
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return model.StaffMemberPage{}, err
		}
		filter.CursorTime = &at
		filter.CursorID = &id
	}

	var records []port.StaffMemberRecord
	if err := s.store.InTx(ctx, func(tx port.Tx) error {
		var err error
		records, err = tx.ListStaffMembers(ctx, filter)
		if err != nil {
			return err
		}
		return tx.Audit(ctx, p.IdentityID, model.ActorRoleStaff, model.AuditMemberList,
			model.AuditObjectMember, uuid.Nil, f.Status)
	}); err != nil {
		return model.StaffMemberPage{}, err
	}

	var nextCursor string
	if len(records) > model.StaffMemberPageSize {
		records = records[:model.StaffMemberPageSize]
		last := records[len(records)-1]
		var sortTime time.Time
		if f.Status == model.StaffMemberStatusVerified && last.VerifiedAt != nil {
			sortTime = *last.VerifiedAt
		} else if f.Status == model.StaffMemberStatusSuspended && last.SuspendedAt != nil {
			sortTime = *last.SuspendedAt
		}
		nextCursor = encodeCursor(sortTime, last.ID)
	}

	items := make([]model.StaffMember, 0, len(records))
	for _, r := range records {
		name, err := s.open(ctx, r.ID, r.SealedName)
		if err != nil {
			return model.StaffMemberPage{}, err
		}
		items = append(items, model.StaffMember{
			ID:          r.ID,
			DisplayName: name,
			DistrictID:  r.DistrictID,
			Verified:    r.Verification == model.VerificationVerified,
			VerifiedAt:  r.VerifiedAt,
			Suspended:   r.SuspendedAt != nil,
			SuspendedAt: r.SuspendedAt,
			Mentor:      slices.Contains(r.Roles, model.RoleMentor),
		})
	}

	s.log().InfoContext(ctx, "staff member list read",
		"event", "StaffMemberListRead",
		"staff_id", p.IdentityID,
		"status", f.Status,
		"count", len(items),
	)

	return model.StaffMemberPage{
		Items:      items,
		NextCursor: nextCursor,
	}, nil
}

func (s *Service) log() *slog.Logger {
	return slog.Default()
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + "." + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	bad := fmt.Errorf("%w: cursor", model.ErrInvalidRequest)
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

// mentorRoleChanged is the payload of membership.MentorRoleChanged v1.
type mentorRoleChanged struct {
	MemberID uuid.UUID `json:"member_id"`
	Mentor   bool      `json:"mentor"`
}

// GrantMentorRole gives a verified member the mentor role.
// Staff moderator or admin with MFA (deny by default). Member must exist (404),
// be verified and not suspended (409). Idempotent when she already holds the
// mentor role. Writes audit log (membership.member.mentor_grant) and outbox event
// membership.MentorRoleChanged (v1) only when the role actually changed.
func (s *Service) GrantMentorRole(ctx context.Context, memberID uuid.UUID) error {
	p, err := staffModerator(ctx)
	if err != nil {
		return err
	}
	if memberID == uuid.Nil {
		return model.ErrMemberNotFound
	}
	now := s.clock.Now().UTC()
	return s.store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.LockMember(ctx, memberID); err != nil {
			return err
		}
		rec, err := tx.MemberByID(ctx, memberID)
		if err != nil {
			return err
		}
		if rec.Verification != model.VerificationVerified {
			return model.ErrNotVerified
		}
		if rec.SuspendedAt != nil {
			return model.ErrSuspended
		}
		if slices.Contains(rec.Roles, model.RoleMentor) {
			return nil
		}
		newRoles := append(slices.Clone(rec.Roles), model.RoleMentor)
		if err := tx.SetRoles(ctx, memberID, newRoles, now); err != nil {
			return err
		}
		if err := tx.Audit(ctx, p.IdentityID, model.ActorRoleStaff, model.AuditMentorGrant,
			model.AuditObjectMember, memberID, ""); err != nil {
			return err
		}
		return tx.Publish(ctx, now, outbox.Event{
			Type:    model.EventMentorRoleChanged,
			Version: 1,
			Data:    mentorRoleChanged{MemberID: memberID, Mentor: true},
		})
	})
}

// RevokeMentorRole takes the mentor role away.
// Staff moderator or admin with MFA (deny by default). Unknown member answers 204
// idempotently without creating anything. Writes audit log (membership.member.mentor_revoke)
// and outbox event membership.MentorRoleChanged (v1, mentor=false) only when the role
// actually changed.
func (s *Service) RevokeMentorRole(ctx context.Context, memberID uuid.UUID) error {
	p, err := staffModerator(ctx)
	if err != nil {
		return err
	}
	if memberID == uuid.Nil {
		return nil
	}
	now := s.clock.Now().UTC()
	return s.store.InTx(ctx, func(tx port.Tx) error {
		if err := tx.LockMember(ctx, memberID); err != nil {
			if errors.Is(err, model.ErrMemberNotFound) {
				return nil
			}
			return err
		}
		rec, err := tx.MemberByID(ctx, memberID)
		if errors.Is(err, model.ErrMemberNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if !slices.Contains(rec.Roles, model.RoleMentor) {
			return nil
		}
		newRoles := make([]string, 0, len(rec.Roles))
		for _, r := range rec.Roles {
			if r != model.RoleMentor {
				newRoles = append(newRoles, r)
			}
		}
		if err := tx.SetRoles(ctx, memberID, newRoles, now); err != nil {
			return err
		}
		if err := tx.Audit(ctx, p.IdentityID, model.ActorRoleStaff, model.AuditMentorRevoke,
			model.AuditObjectMember, memberID, ""); err != nil {
			return err
		}
		return tx.Publish(ctx, now, outbox.Event{
			Type:    model.EventMentorRoleChanged,
			Version: 1,
			Data:    mentorRoleChanged{MemberID: memberID, Mentor: false},
		})
	})
}
