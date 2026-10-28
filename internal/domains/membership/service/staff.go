package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
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
