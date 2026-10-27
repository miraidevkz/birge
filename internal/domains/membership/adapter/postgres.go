// Package adapter implements the membership ports on PostgreSQL (sqlc store)
// and the platform outbox.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/membership/model"
	"birge/internal/domains/membership/port"
	"birge/internal/domains/membership/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
)

// Postgres is the membership store.
type Postgres struct {
	Pool *pgxpool.Pool
}

var _ port.Store = (*Postgres)(nil)

// InTx runs fn in one transaction.
func (p *Postgres) InTx(ctx context.Context, fn func(port.Tx) error) error {
	return db.InTx(ctx, p.Pool, func(tx pgx.Tx) error {
		return fn(&pgTx{q: store.New(tx), outbox: platformstore.New(tx)})
	})
}

type pgTx struct {
	q      *store.Queries
	outbox *platformstore.Queries
}

var _ port.Tx = (*pgTx)(nil)

// MemberByIdentity returns the member for identityID or model.ErrMemberNotFound.
func (t *pgTx) MemberByIdentity(ctx context.Context, identityID uuid.UUID) (port.MemberRecord, error) {
	r, err := t.q.GetMemberByIdentity(ctx, identityID)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.MemberRecord{}, model.ErrMemberNotFound
	}
	if err != nil {
		return port.MemberRecord{}, fmt.Errorf("membership: get member by identity: %w", err)
	}
	return memberRecord(store.GetMemberByIDRow(r)), nil
}

// MemberByID returns the member or model.ErrMemberNotFound.
func (t *pgTx) MemberByID(ctx context.Context, id uuid.UUID) (port.MemberRecord, error) {
	r, err := t.q.GetMemberByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.MemberRecord{}, model.ErrMemberNotFound
	}
	if err != nil {
		return port.MemberRecord{}, fmt.Errorf("membership: get member: %w", err)
	}
	return memberRecord(r), nil
}

// ListVerifiedInDistrict returns the IDs of verified members in districtID.
func (t *pgTx) ListVerifiedInDistrict(ctx context.Context, districtID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := t.q.ListVerifiedInDistrict(ctx, &districtID)
	if err != nil {
		return nil, fmt.Errorf("membership: list verified in district: %w", err)
	}
	return ids, nil
}

func memberRecord(r store.GetMemberByIDRow) port.MemberRecord {
	return port.MemberRecord{
		ID: r.ID, IdentityID: r.IdentityID, SealedName: r.DisplayName, Lang: model.Lang(r.Lang),
		CityID: r.CityID, DistrictID: r.DistrictID, Verification: model.Verification(r.Verification),
		Roles: r.Roles,
	}
}

// LockMember locks the member row until the end of the transaction.
func (t *pgTx) LockMember(ctx context.Context, id uuid.UUID) error {
	_, err := t.q.LockMember(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("membership: lock member: %w", err)
	}
	return nil
}

// InsertMember creates the member for identityID; created is false when it
// already exists (unique identity_id races resolve to one row).
func (t *pgTx) InsertMember(ctx context.Context, identityID uuid.UUID, now time.Time) (uuid.UUID, bool, error) {
	id, err := t.q.InsertMember(ctx, store.InsertMemberParams{IdentityID: identityID, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("membership: insert member: %w", err)
	}
	return id, true, nil
}

// UpdateProfile applies the set fields of p; returns model.ErrMemberNotFound when id is absent.
func (t *pgTx) UpdateProfile(ctx context.Context, id uuid.UUID, p port.ProfilePatch, now time.Time) error {
	var lang *string
	if p.Lang != nil {
		l := string(*p.Lang)
		lang = &l
	}
	n, err := t.q.UpdateMemberProfile(ctx, store.UpdateMemberProfileParams{
		DisplayName: p.SealedName, Lang: lang, DistrictID: p.DistrictID, Now: now, ID: id,
	})
	if err != nil {
		return fmt.Errorf("membership: update profile: %w", err)
	}
	if n == 0 {
		return model.ErrMemberNotFound
	}
	return nil
}

// SetVerification writes the member's verification state.
func (t *pgTx) SetVerification(ctx context.Context, id uuid.UUID, v model.Verification, now time.Time) error {
	n, err := t.q.SetMemberVerification(ctx, store.SetMemberVerificationParams{Verification: string(v), Now: now, ID: id})
	if err != nil {
		return fmt.Errorf("membership: set verification: %w", err)
	}
	if n == 0 {
		return model.ErrMemberNotFound
	}
	return nil
}

// Children lists the member's child cards.
func (t *pgTx) Children(ctx context.Context, memberID uuid.UUID) ([]model.Child, error) {
	rows, err := t.q.ListChildren(ctx, memberID)
	if err != nil {
		return nil, fmt.Errorf("membership: list children: %w", err)
	}
	out := make([]model.Child, 0, len(rows))
	for _, r := range rows {
		out = append(out, child(r.ID, r.BirthYear, r.Needs))
	}
	return out, nil
}

// CountChildren returns how many child cards the member has.
func (t *pgTx) CountChildren(ctx context.Context, memberID uuid.UUID) (int, error) {
	n, err := t.q.CountChildren(ctx, memberID)
	if err != nil {
		return 0, fmt.Errorf("membership: count children: %w", err)
	}
	return int(n), nil
}

// ChildByIdempotencyKey returns the child created with key or model.ErrChildNotFound.
func (t *pgTx) ChildByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Child, error) {
	r, err := t.q.GetChildByIdempotencyKey(ctx, store.GetChildByIdempotencyKeyParams{MemberID: memberID, IdempotencyKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Child{}, model.ErrChildNotFound
	}
	if err != nil {
		return model.Child{}, fmt.Errorf("membership: get child by key: %w", err)
	}
	return child(r.ID, r.BirthYear, r.Needs), nil
}

// InsertChild creates a child card keyed by the request Idempotency-Key.
func (t *pgTx) InsertChild(ctx context.Context, memberID, key uuid.UUID, c model.ChildInput, now time.Time) (model.Child, error) {
	r, err := t.q.InsertChild(ctx, store.InsertChildParams{
		MemberID: memberID, IdempotencyKey: key, BirthYear: int32(c.BirthYear), Needs: needs(c.Needs), Now: now, //nolint:gosec // validated to 1990..2100
	})
	if err != nil {
		return model.Child{}, fmt.Errorf("membership: insert child: %w", err)
	}
	return child(r.ID, r.BirthYear, r.Needs), nil
}

// UpdateChild replaces a child card that belongs to memberID or returns model.ErrChildNotFound.
func (t *pgTx) UpdateChild(ctx context.Context, memberID, childID uuid.UUID, c model.ChildInput, now time.Time) (model.Child, error) {
	r, err := t.q.UpdateChild(ctx, store.UpdateChildParams{
		BirthYear: int32(c.BirthYear), Needs: needs(c.Needs), Now: now, ID: childID, MemberID: memberID, //nolint:gosec // validated to 1990..2100
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Child{}, model.ErrChildNotFound
	}
	if err != nil {
		return model.Child{}, fmt.Errorf("membership: update child: %w", err)
	}
	return child(r.ID, r.BirthYear, r.Needs), nil
}

// DeleteChild removes a child of memberID and reports whether a row was deleted.
func (t *pgTx) DeleteChild(ctx context.Context, memberID, childID uuid.UUID) (bool, error) {
	n, err := t.q.DeleteChild(ctx, store.DeleteChildParams{ID: childID, MemberID: memberID})
	if err != nil {
		return false, fmt.Errorf("membership: delete child: %w", err)
	}
	return n > 0, nil
}

// InsertPendingVerification creates a pending request; created is false while one is pending.
func (t *pgTx) InsertPendingVerification(ctx context.Context, memberID uuid.UUID, now time.Time) (uuid.UUID, bool, error) {
	id, err := t.q.InsertPendingVerificationRequest(ctx, store.InsertPendingVerificationRequestParams{MemberID: memberID, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("membership: insert verification request: %w", err)
	}
	return id, true, nil
}

// InsertBlock records a block; returns false when the block already exists.
func (t *pgTx) InsertBlock(ctx context.Context, blocker, blocked uuid.UUID, now time.Time) (bool, error) {
	n, err := t.q.InsertBlock(ctx, store.InsertBlockParams{Blocker: blocker, Blocked: blocked, Since: now})
	if err != nil {
		return false, fmt.Errorf("membership: insert block: %w", err)
	}
	return n > 0, nil
}

// DeleteBlock removes a block if present (idempotent).
func (t *pgTx) DeleteBlock(ctx context.Context, blocker, blocked uuid.UUID) error {
	if err := t.q.DeleteBlock(ctx, store.DeleteBlockParams{Blocker: blocker, Blocked: blocked}); err != nil {
		return fmt.Errorf("membership: delete block: %w", err)
	}
	return nil
}

// CountBlocks returns how many members blocker has blocked.
func (t *pgTx) CountBlocks(ctx context.Context, blocker uuid.UUID) (int, error) {
	n, err := t.q.CountBlocks(ctx, blocker)
	if err != nil {
		return 0, fmt.Errorf("membership: count blocks: %w", err)
	}
	return int(n), nil
}

// Blocks lists members blocked by blocker, newest first, capped at limit.
func (t *pgTx) Blocks(ctx context.Context, blocker uuid.UUID, limit int) ([]port.BlockRecord, error) {
	rows, err := t.q.ListBlocks(ctx, store.ListBlocksParams{Blocker: blocker, MaxRows: int32(min(limit, model.MaxBlocks))}) //nolint:gosec // capped at MaxBlocks
	if err != nil {
		return nil, fmt.Errorf("membership: list blocks: %w", err)
	}
	out := make([]port.BlockRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, port.BlockRecord{Blocked: r.Blocked, SealedName: r.DisplayName, Since: r.Since})
	}
	return out, nil
}

// IsBlocked reports a block between a and b in either direction.
func (t *pgTx) IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	blocked, err := t.q.IsBlocked(ctx, store.IsBlockedParams{A: a, B: b})
	if err != nil {
		return false, fmt.Errorf("membership: is blocked: %w", err)
	}
	return blocked, nil
}

// DistrictExists reports whether id is a known district.
func (t *pgTx) DistrictExists(ctx context.Context, id uuid.UUID) (bool, error) {
	ok, err := t.q.DistrictExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("membership: district exists: %w", err)
	}
	return ok, nil
}

// CountKnownNeeds returns how many of the distinct codes exist in the reference table.
func (t *pgTx) CountKnownNeeds(ctx context.Context, codes []string) (int, error) {
	n, err := t.q.CountKnownNeeds(ctx, codes)
	if err != nil {
		return 0, fmt.Errorf("membership: count needs: %w", err)
	}
	return int(n), nil
}

// Cities returns the deployment's cities with their districts.
func (t *pgTx) Cities(ctx context.Context) ([]model.City, error) {
	cities, err := t.q.ListCities(ctx)
	if err != nil {
		return nil, fmt.Errorf("membership: list cities: %w", err)
	}
	districts, err := t.q.ListDistricts(ctx)
	if err != nil {
		return nil, fmt.Errorf("membership: list districts: %w", err)
	}
	byCity := make(map[uuid.UUID][]model.District, len(cities))
	for _, d := range districts {
		byCity[d.CityID] = append(byCity[d.CityID], model.District{ID: d.ID, Code: d.Code})
	}
	out := make([]model.City, 0, len(cities))
	for _, c := range cities {
		ds := byCity[c.ID]
		if ds == nil {
			ds = []model.District{}
		}
		out = append(out, model.City{ID: c.ID, Code: c.Code, Districts: ds})
	}
	return out, nil
}

// Needs returns the deployment's needs codes.
func (t *pgTx) Needs(ctx context.Context) ([]string, error) {
	codes, err := t.q.ListNeeds(ctx)
	if err != nil {
		return nil, fmt.Errorf("membership: list needs: %w", err)
	}
	return needs(codes), nil
}

// SeedReferenceIfEmpty inserts cities, districts and needs when every
// membership reference table is empty. It never overwrites existing rows.
// seeded is true only when this call inserted the seed.
func (p *Postgres) SeedReferenceIfEmpty(ctx context.Context, ref model.ReferenceSeed) (seeded bool, err error) {
	if len(ref.Cities) == 0 || len(ref.Needs) == 0 {
		return false, fmt.Errorf("membership: reference seed needs cities and needs")
	}
	err = db.InTx(ctx, p.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		empty, err := q.ReferenceIsEmpty(ctx)
		if err != nil {
			return fmt.Errorf("membership: reference empty check: %w", err)
		}
		if !empty {
			return nil
		}
		for _, c := range ref.Cities {
			if c.Code == "" || len(c.Districts) == 0 {
				return fmt.Errorf("membership: reference seed city needs a code and districts")
			}
			id, err := q.InsertCity(ctx, c.Code)
			if err != nil {
				return fmt.Errorf("membership: insert city: %w", err)
			}
			for _, d := range c.Districts {
				if err := q.InsertDistrict(ctx, store.InsertDistrictParams{CityID: id, Code: d}); err != nil {
					return fmt.Errorf("membership: insert district: %w", err)
				}
			}
		}
		for _, n := range ref.Needs {
			if err := q.InsertNeed(ctx, n); err != nil {
				return fmt.Errorf("membership: insert need: %w", err)
			}
		}
		seeded = true
		return nil
	})
	return seeded, err
}

// Publish stores e in the platform outbox in this transaction.
func (t *pgTx) Publish(ctx context.Context, now time.Time, e outbox.Event) error {
	_, err := outbox.Enqueue(ctx, t.outbox, now, "", e)
	return err
}

func child(id uuid.UUID, birthYear int32, n []string) model.Child {
	return model.Child{ID: id, BirthYear: int(birthYear), Needs: needs(n)}
}

// needs never returns nil, so empty lists stay empty arrays in SQL and JSON.
func needs(n []string) []string {
	if n == nil {
		return []string{}
	}
	return n
}
