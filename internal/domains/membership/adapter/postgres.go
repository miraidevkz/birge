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

func memberRecord(r store.GetMemberByIDRow) port.MemberRecord {
	return port.MemberRecord{
		ID: r.ID, IdentityID: r.IdentityID, SealedName: r.DisplayName, Lang: model.Lang(r.Lang),
		CityID: r.CityID, DistrictID: r.DistrictID, Verification: model.Verification(r.Verification),
		Roles: r.Roles,
	}
}

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

func (t *pgTx) CountChildren(ctx context.Context, memberID uuid.UUID) (int, error) {
	n, err := t.q.CountChildren(ctx, memberID)
	if err != nil {
		return 0, fmt.Errorf("membership: count children: %w", err)
	}
	return int(n), nil
}

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

func (t *pgTx) InsertChild(ctx context.Context, memberID, key uuid.UUID, c model.ChildInput, now time.Time) (model.Child, error) {
	r, err := t.q.InsertChild(ctx, store.InsertChildParams{
		MemberID: memberID, IdempotencyKey: key, BirthYear: int32(c.BirthYear), Needs: needs(c.Needs), Now: now, //nolint:gosec // validated to 1990..2100
	})
	if err != nil {
		return model.Child{}, fmt.Errorf("membership: insert child: %w", err)
	}
	return child(r.ID, r.BirthYear, r.Needs), nil
}

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

func (t *pgTx) DeleteChild(ctx context.Context, memberID, childID uuid.UUID) (bool, error) {
	n, err := t.q.DeleteChild(ctx, store.DeleteChildParams{ID: childID, MemberID: memberID})
	if err != nil {
		return false, fmt.Errorf("membership: delete child: %w", err)
	}
	return n > 0, nil
}

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

func (t *pgTx) InsertBlock(ctx context.Context, blocker, blocked uuid.UUID, now time.Time) (bool, error) {
	n, err := t.q.InsertBlock(ctx, store.InsertBlockParams{Blocker: blocker, Blocked: blocked, Since: now})
	if err != nil {
		return false, fmt.Errorf("membership: insert block: %w", err)
	}
	return n > 0, nil
}

func (t *pgTx) DeleteBlock(ctx context.Context, blocker, blocked uuid.UUID) error {
	if err := t.q.DeleteBlock(ctx, store.DeleteBlockParams{Blocker: blocker, Blocked: blocked}); err != nil {
		return fmt.Errorf("membership: delete block: %w", err)
	}
	return nil
}

func (t *pgTx) CountBlocks(ctx context.Context, blocker uuid.UUID) (int, error) {
	n, err := t.q.CountBlocks(ctx, blocker)
	if err != nil {
		return 0, fmt.Errorf("membership: count blocks: %w", err)
	}
	return int(n), nil
}

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

func (t *pgTx) IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error) {
	blocked, err := t.q.IsBlocked(ctx, store.IsBlockedParams{A: a, B: b})
	if err != nil {
		return false, fmt.Errorf("membership: is blocked: %w", err)
	}
	return blocked, nil
}

func (t *pgTx) DistrictExists(ctx context.Context, id uuid.UUID) (bool, error) {
	ok, err := t.q.DistrictExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("membership: district exists: %w", err)
	}
	return ok, nil
}

func (t *pgTx) CountKnownNeeds(ctx context.Context, codes []string) (int, error) {
	n, err := t.q.CountKnownNeeds(ctx, codes)
	if err != nil {
		return 0, fmt.Errorf("membership: count needs: %w", err)
	}
	return int(n), nil
}

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

func (t *pgTx) Needs(ctx context.Context) ([]string, error) {
	codes, err := t.q.ListNeeds(ctx)
	if err != nil {
		return nil, fmt.Errorf("membership: list needs: %w", err)
	}
	return needs(codes), nil
}

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
