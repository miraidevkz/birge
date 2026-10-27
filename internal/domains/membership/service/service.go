// Package service implements the membership use cases. Methods that act for the
// signed-in member take the principal from the context (internal/platform/auth)
// and only ever touch that member's data. IsBlocked and Member are the API for
// other contexts.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
	"birge/internal/platform/outbox"
)

// Service implements the membership use cases.
type Service struct {
	store port.Store
	enc   port.Encryptor
	clock clock.Clock
}

// New returns a Service. All dependencies are required.
func New(store port.Store, enc port.Encryptor, clk clock.Clock) *Service {
	if store == nil || enc == nil || clk == nil {
		panic("membership: service needs a store, an encryptor and a clock")
	}
	return &Service{store: store, enc: enc, clock: clk}
}

// Event payloads (contracts/events/membership.*.v1.json): IDs only.
type memberRegistered struct {
	MemberID uuid.UUID `json:"member_id"`
}

type verificationRequested struct {
	MemberID  uuid.UUID `json:"member_id"`
	RequestID uuid.UUID `json:"request_id"`
}

// Me returns the signed-in member, creating her on the first call.
func (s *Service) Me(ctx context.Context) (model.Member, error) {
	return s.withMe(ctx, func(_ port.Tx, me port.MemberRecord, _ time.Time) (port.MemberRecord, error) {
		return me, nil
	})
}

// UpdateMe changes the signed-in member's profile.
func (s *Service) UpdateMe(ctx context.Context, u model.ProfileUpdate) (model.Member, error) {
	u, err := u.Normalize()
	if err != nil {
		return model.Member{}, err
	}
	return s.withMe(ctx, func(tx port.Tx, me port.MemberRecord, now time.Time) (port.MemberRecord, error) {
		patch := port.ProfilePatch{Lang: u.Lang, DistrictID: u.DistrictID}
		if u.DistrictID != nil {
			ok, err := tx.DistrictExists(ctx, *u.DistrictID)
			if err != nil {
				return me, err
			}
			if !ok {
				return me, model.ErrUnknownDistrict
			}
		}
		if u.DisplayName != nil {
			sealed, err := s.enc.Seal(ctx, me.ID, []byte(*u.DisplayName))
			if err != nil {
				return me, fmt.Errorf("membership: seal display name: %w", err)
			}
			patch.SealedName = sealed
		}
		if err := tx.UpdateProfile(ctx, me.ID, patch, now); err != nil {
			return me, err
		}
		return tx.MemberByID(ctx, me.ID)
	})
}

// AddChild adds a child to the signed-in member. Repeating a request with the
// same idempotency key returns the child created by the first one.
func (s *Service) AddChild(ctx context.Context, key uuid.UUID, in model.ChildInput) (model.Child, error) {
	if key == uuid.Nil {
		return model.Child{}, model.ErrInvalidIdempotencyKey
	}
	in, err := in.Normalize()
	if err != nil {
		return model.Child{}, err
	}
	var child model.Child
	err = s.inMyTx(ctx, func(tx port.Tx, me port.MemberRecord, now time.Time) error {
		// Serializes concurrent adds of one member for the limit and the key check.
		if err := tx.LockMember(ctx, me.ID); err != nil {
			return err
		}
		existing, err := tx.ChildByIdempotencyKey(ctx, me.ID, key)
		if err == nil {
			child = existing
			return nil
		}
		if !errors.Is(err, model.ErrChildNotFound) {
			return err
		}
		if err := checkNeeds(ctx, tx, in.Needs); err != nil {
			return err
		}
		n, err := tx.CountChildren(ctx, me.ID)
		if err != nil {
			return err
		}
		if n >= model.MaxChildren {
			return model.ErrChildrenLimit
		}
		child, err = tx.InsertChild(ctx, me.ID, key, in, now)
		return err
	})
	return child, err
}

// UpdateChild replaces a child card of the signed-in member.
func (s *Service) UpdateChild(ctx context.Context, childID uuid.UUID, in model.ChildInput) (model.Child, error) {
	in, err := in.Normalize()
	if err != nil {
		return model.Child{}, err
	}
	var child model.Child
	err = s.inMyTx(ctx, func(tx port.Tx, me port.MemberRecord, now time.Time) error {
		if err := checkNeeds(ctx, tx, in.Needs); err != nil {
			return err
		}
		c, err := tx.UpdateChild(ctx, me.ID, childID, in, now)
		child = c
		return err
	})
	return child, err
}

// DeleteChild removes a child card of the signed-in member.
func (s *Service) DeleteChild(ctx context.Context, childID uuid.UUID) error {
	return s.inMyTx(ctx, func(tx port.Tx, me port.MemberRecord, _ time.Time) error {
		ok, err := tx.DeleteChild(ctx, me.ID, childID)
		if err != nil {
			return err
		}
		if !ok {
			return model.ErrChildNotFound
		}
		return nil
	})
}

// RequestVerification asks a moderator for a verification call. It is
// idempotent while a request is pending: one request, one event.
func (s *Service) RequestVerification(ctx context.Context) (model.Member, error) {
	return s.withMe(ctx, func(tx port.Tx, me port.MemberRecord, now time.Time) (port.MemberRecord, error) {
		if me.Verification == model.VerificationVerified {
			return me, model.ErrAlreadyVerified
		}
		reqID, created, err := tx.InsertPendingVerification(ctx, me.ID, now)
		if err != nil {
			return me, err
		}
		if !created {
			// Re-read: a concurrent request may have committed after me was read.
			return tx.MemberByID(ctx, me.ID)
		}
		if err := tx.SetVerification(ctx, me.ID, model.VerificationPending, now); err != nil {
			return me, err
		}
		err = tx.Publish(ctx, now, outbox.Event{
			Type: model.EventVerificationRequested, Version: 1,
			Data: verificationRequested{MemberID: me.ID, RequestID: reqID},
		})
		if err != nil {
			return me, err
		}
		return tx.MemberByID(ctx, me.ID)
	})
}

// Block blocks another member (ADR-0019). Idempotent; the other member is not told.
func (s *Service) Block(ctx context.Context, memberID uuid.UUID) error {
	return s.inMyTx(ctx, func(tx port.Tx, me port.MemberRecord, now time.Time) error {
		if memberID == me.ID {
			return model.ErrSelfBlock
		}
		if _, err := tx.MemberByID(ctx, memberID); err != nil {
			return err
		}
		// Serializes concurrent blocks of one member for the limit.
		if err := tx.LockMember(ctx, me.ID); err != nil {
			return err
		}
		created, err := tx.InsertBlock(ctx, me.ID, memberID, now)
		if err != nil || !created {
			return err
		}
		n, err := tx.CountBlocks(ctx, me.ID)
		if err != nil {
			return err
		}
		if n > model.MaxBlocks {
			return model.ErrBlocksLimit // rolls the insert back
		}
		return nil
	})
}

// Unblock removes a block. Idempotent.
func (s *Service) Unblock(ctx context.Context, memberID uuid.UUID) error {
	return s.inMyTx(ctx, func(tx port.Tx, me port.MemberRecord, _ time.Time) error {
		if memberID == me.ID {
			return model.ErrSelfBlock
		}
		return tx.DeleteBlock(ctx, me.ID, memberID)
	})
}

// Blocks lists the members blocked by the signed-in member, newest first.
func (s *Service) Blocks(ctx context.Context) ([]model.BlockedMember, error) {
	var out []model.BlockedMember
	err := s.inMyTx(ctx, func(tx port.Tx, me port.MemberRecord, _ time.Time) error {
		recs, err := tx.Blocks(ctx, me.ID, model.MaxBlocks)
		if err != nil {
			return err
		}
		out = make([]model.BlockedMember, 0, len(recs))
		for _, r := range recs {
			name, err := s.open(ctx, r.Blocked, r.SealedName)
			if err != nil {
				return err
			}
			out = append(out, model.BlockedMember{MemberID: r.Blocked, DisplayName: name, Since: r.Since})
		}
		return nil
	})
	return out, err
}

// Cities returns the cities and districts of the deployment. Anonymous.
func (s *Service) Cities(ctx context.Context) ([]model.City, error) {
	var out []model.City
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.Cities(ctx)
		return err
	})
	return out, err
}

// Needs returns the needs codes. Anonymous.
func (s *Service) Needs(ctx context.Context) ([]string, error) {
	var out []string
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.Needs(ctx)
		return err
	})
	return out, err
}

// IsBlocked reports whether a and b blocked each other in either direction.
// For other contexts' block filters (ADR-0019); it does no authorization.
func (s *Service) IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	if a == b {
		return false, nil
	}
	var blocked bool
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		var err error
		blocked, err = tx.IsBlocked(ctx, a, b)
		return err
	})
	return blocked, err
}

// Member returns a member by ID, or model.ErrMemberNotFound. For other
// contexts; it does no authorization. The display name is personal data.
func (s *Service) Member(ctx context.Context, id uuid.UUID) (model.Member, error) {
	var m model.Member
	err := s.store.InTx(ctx, func(tx port.Tx) error {
		rec, err := tx.MemberByID(ctx, id)
		if err != nil {
			return err
		}
		m, err = s.load(ctx, tx, rec)
		return err
	})
	return m, err
}

// withMe runs fn for the signed-in member and returns the member record fn
// returns, with name and children.
func (s *Service) withMe(ctx context.Context, fn func(port.Tx, port.MemberRecord, time.Time) (port.MemberRecord, error)) (model.Member, error) {
	var m model.Member
	err := s.inMyTx(ctx, func(tx port.Tx, me port.MemberRecord, now time.Time) error {
		rec, err := fn(tx, me, now)
		if err != nil {
			return err
		}
		m, err = s.load(ctx, tx, rec)
		return err
	})
	return m, err
}

// inMyTx resolves the principal and runs fn in a transaction with the
// signed-in member, created on first use.
func (s *Service) inMyTx(ctx context.Context, fn func(port.Tx, port.MemberRecord, time.Time) error) error {
	identity, err := identityOf(ctx)
	if err != nil {
		return err
	}
	return s.store.InTx(ctx, func(tx port.Tx) error {
		now := s.clock.Now()
		me, err := s.ensure(ctx, tx, identity, now)
		if err != nil {
			return err
		}
		return fn(tx, me, now)
	})
}

// ensure returns the member of identity, creating it and publishing
// MemberRegistered exactly once (the unique identity_id decides between
// concurrent first calls; the loser reads the winner's row).
func (s *Service) ensure(ctx context.Context, tx port.Tx, identity uuid.UUID, now time.Time) (port.MemberRecord, error) {
	me, err := tx.MemberByIdentity(ctx, identity)
	if !errors.Is(err, model.ErrMemberNotFound) {
		return me, err
	}
	id, created, err := tx.InsertMember(ctx, identity, now)
	if err != nil {
		return me, err
	}
	if created {
		err := tx.Publish(ctx, now, outbox.Event{
			Type: model.EventMemberRegistered, Version: 1, Data: memberRegistered{MemberID: id},
		})
		if err != nil {
			return me, err
		}
	}
	return tx.MemberByIdentity(ctx, identity)
}

func (s *Service) load(ctx context.Context, tx port.Tx, rec port.MemberRecord) (model.Member, error) {
	name, err := s.open(ctx, rec.ID, rec.SealedName)
	if err != nil {
		return model.Member{}, err
	}
	children, err := tx.Children(ctx, rec.ID)
	if err != nil {
		return model.Member{}, err
	}
	return model.Member{
		ID: rec.ID, IdentityID: rec.IdentityID, DisplayName: name, Lang: rec.Lang,
		CityID: rec.CityID, DistrictID: rec.DistrictID, Verification: rec.Verification,
		Roles: rec.Roles, Children: children,
	}, nil
}

func (s *Service) open(ctx context.Context, memberID uuid.UUID, sealed []byte) (string, error) {
	if sealed == nil {
		return "", nil
	}
	plain, err := s.enc.Open(ctx, memberID, sealed)
	if err != nil {
		return "", fmt.Errorf("membership: open display name: %w", err)
	}
	return string(plain), nil
}

func checkNeeds(ctx context.Context, tx port.Tx, needs []string) error {
	if len(needs) == 0 {
		return nil
	}
	n, err := tx.CountKnownNeeds(ctx, needs)
	if err != nil {
		return err
	}
	if n != len(needs) {
		return model.ErrUnknownNeed
	}
	return nil
}

func identityOf(ctx context.Context) (uuid.UUID, error) {
	p, ok := auth.FromContext(ctx)
	if !ok || p.IdentityID == uuid.Nil {
		return uuid.Nil, model.ErrUnauthenticated
	}
	if p.Staff {
		return uuid.Nil, model.ErrMemberSessionRequired
	}
	return p.IdentityID, nil
}
