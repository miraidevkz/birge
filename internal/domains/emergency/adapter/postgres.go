// Package adapter implements the emergency ports on PostgreSQL (schema
// emergency, sqlc in ../store) and the platform hooks it needs: outbox
// events, the SOS auto-close and plan-link purge timers, the audit log and
// the timer handlers.
// Ports onto other contexts (membership profiles and blocks, the circle's
// "met in person") are wired later.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	emergencystore "birge/internal/domains/emergency/store"
	"birge/internal/platform/db"
	"birge/internal/platform/outbox"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

// PostgresStore implements port.Store on PostgreSQL.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// InTx runs fn in a transaction (READ COMMITTED, the PostgreSQL default;
// limit checks take an advisory lock through Tx.Lock).
func (s *PostgresStore) InTx(ctx context.Context, fn func(tx port.Tx) error) error {
	return db.InTx(ctx, s.Pool, func(pgxTx pgx.Tx) error {
		return fn(Bind(pgxTx))
	})
}

// Bind returns a port.Tx working inside an existing transaction, such as the
// one the timer worker passes to a handler.
func Bind(tx pgx.Tx) port.Tx {
	return &postgresTx{tx: tx, q: emergencystore.New(tx), platform: platformstore.New(tx)}
}

type postgresTx struct {
	tx       pgx.Tx
	q        *emergencystore.Queries
	platform *platformstore.Queries
}

// notFound maps pgx.ErrNoRows to model.ErrNotFound and wraps other errors.
func notFound(err error, op string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

// Lock takes a transaction-scoped advisory lock on (scope, memberID).
func (t *postgresTx) Lock(ctx context.Context, scope string, memberID uuid.UUID) error {
	if err := t.q.AdvisoryXactLock(ctx, scope+"|"+memberID.String()); err != nil {
		return fmt.Errorf("lock %s: %w", scope, err)
	}
	return nil
}

// CountSosSince counts the member's SOS created after since.
func (t *postgresTx) CountSosSince(ctx context.Context, memberID uuid.UUID, since time.Time) (int, error) {
	n, err := t.q.CountSosSince(ctx, emergencystore.CountSosSinceParams{MemberID: memberID, Since: since})
	if err != nil {
		return 0, fmt.Errorf("count sos: %w", err)
	}
	return int(n), nil
}

// InsertSos inserts an open SOS; inserted is false on a repeated idempotency key.
func (t *postgresTx) InsertSos(ctx context.Context, s model.Sos) (model.Sos, bool, error) {
	row, err := t.q.InsertSos(ctx, emergencystore.InsertSosParams{
		ID: s.ID, MemberID: s.MemberID, IdempotencyKey: s.IdempotencyKey, Type: s.Type, Content: s.Text,
		CreatedAt: s.CreatedAt, ClosesAt: s.ClosesAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Sos{}, false, nil
	}
	if err != nil {
		return model.Sos{}, false, fmt.Errorf("insert sos: %w", err)
	}
	return mapSos(row), true, nil
}

// GetSosByIdempotencyKey returns the member's SOS raised with key, or model.ErrNotFound.
func (t *postgresTx) GetSosByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Sos, error) {
	row, err := t.q.GetSosByIdempotencyKey(ctx, emergencystore.GetSosByIdempotencyKeyParams{MemberID: memberID, IdempotencyKey: key})
	if err != nil {
		return model.Sos{}, notFound(err, "get sos by key")
	}
	return t.withCount(ctx, mapSos(row))
}

// GetSos returns the SOS with its response count, optionally locked.
func (t *postgresTx) GetSos(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Sos, error) {
	var row emergencystore.EmergencySosRequest
	var err error
	if forUpdate {
		row, err = t.q.GetSosForUpdate(ctx, id)
	} else {
		row, err = t.q.GetSos(ctx, id)
	}
	if err != nil {
		return model.Sos{}, notFound(err, "get sos")
	}
	return t.withCount(ctx, mapSos(row))
}

func (t *postgresTx) withCount(ctx context.Context, s model.Sos) (model.Sos, error) {
	n, err := t.q.CountSosResponses(ctx, s.ID)
	if err != nil {
		return model.Sos{}, fmt.Errorf("count responses: %w", err)
	}
	s.ResponseCount = int(n)
	return s, nil
}

// ListMySos returns the member's latest SOS with response counts.
func (t *postgresTx) ListMySos(ctx context.Context, memberID uuid.UUID, limit int32) ([]model.Sos, error) {
	rows, err := t.q.ListMySos(ctx, emergencystore.ListMySosParams{MemberID: memberID, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list my sos: %w", err)
	}
	out := make([]model.Sos, 0, len(rows))
	for _, r := range rows {
		s := mapSos(emergencystore.EmergencySosRequest{
			ID: r.ID, MemberID: r.MemberID, IdempotencyKey: r.IdempotencyKey, Type: r.Type, Content: r.Content,
			Status: r.Status, CreatedAt: r.CreatedAt, ClosesAt: r.ClosesAt, DistrictID: r.DistrictID,
			SharedAt: r.SharedAt, ClosedAt: r.ClosedAt, CloseReason: r.CloseReason,
			SeenAt: r.SeenAt, SeenBy: r.SeenBy, ClosedBy: r.ClosedBy,
		})
		s.ResponseCount = int(r.ResponseCount)
		out = append(out, s)
	}
	return out, nil
}

// ShareSos shares an open, unshared, non-childcare SOS; updated is false otherwise.
func (t *postgresTx) ShareSos(ctx context.Context, id, districtID uuid.UUID, at time.Time) (model.Sos, bool, error) {
	row, err := t.q.ShareSos(ctx, emergencystore.ShareSosParams{DistrictID: &districtID, SharedAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Sos{}, false, nil
	}
	if err != nil {
		return model.Sos{}, false, fmt.Errorf("share sos: %w", err)
	}
	return mapSos(row), true, nil
}

// CloseSos closes an open SOS; updated is false when it was closed.
func (t *postgresTx) CloseSos(ctx context.Context, id uuid.UUID, reason string, closedBy *uuid.UUID, at time.Time) (model.Sos, bool, error) {
	row, err := t.q.CloseSos(ctx, emergencystore.CloseSosParams{ClosedAt: &at, CloseReason: &reason, ClosedBy: closedBy, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Sos{}, false, nil
	}
	if err != nil {
		return model.Sos{}, false, fmt.Errorf("close sos: %w", err)
	}
	return mapSos(row), true, nil
}

// MarkSosSeen records the first "Seen"; updated is false when already seen.
func (t *postgresTx) MarkSosSeen(ctx context.Context, id, staffID uuid.UUID, at time.Time) (model.Sos, bool, error) {
	row, err := t.q.MarkSosSeen(ctx, emergencystore.MarkSosSeenParams{SeenAt: &at, SeenBy: &staffID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Sos{}, false, nil
	}
	if err != nil {
		return model.Sos{}, false, fmt.Errorf("mark sos seen: %w", err)
	}
	return mapSos(row), true, nil
}

// ListStaffSos returns one page of the moderator queue with response counts.
func (t *postgresTx) ListStaffSos(ctx context.Context, f model.StaffSosFilter) ([]model.Sos, error) {
	var rows []emergencystore.ListStaffOpenSosRow
	switch f.Status {
	case model.SosOpen:
		var err error
		rows, err = t.q.ListStaffOpenSos(ctx, emergencystore.ListStaffOpenSosParams{
			CursorCreatedAt: f.CursorCreatedAt, CursorID: f.CursorID, PageLimit: f.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list staff open sos: %w", err)
		}
	case model.SosClosed:
		closed, err := t.q.ListStaffClosedSos(ctx, emergencystore.ListStaffClosedSosParams{
			CursorCreatedAt: f.CursorCreatedAt, CursorID: f.CursorID, PageLimit: f.Limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list staff closed sos: %w", err)
		}
		rows = make([]emergencystore.ListStaffOpenSosRow, 0, len(closed))
		for _, r := range closed {
			rows = append(rows, emergencystore.ListStaffOpenSosRow(r))
		}
	default:
		return nil, fmt.Errorf("list staff sos: unknown status %q", f.Status)
	}
	out := make([]model.Sos, 0, len(rows))
	for _, r := range rows {
		s := mapSos(emergencystore.EmergencySosRequest{
			ID: r.ID, MemberID: r.MemberID, IdempotencyKey: r.IdempotencyKey, Type: r.Type, Content: r.Content,
			Status: r.Status, CreatedAt: r.CreatedAt, ClosesAt: r.ClosesAt, DistrictID: r.DistrictID,
			SharedAt: r.SharedAt, ClosedAt: r.ClosedAt, CloseReason: r.CloseReason,
			SeenAt: r.SeenAt, SeenBy: r.SeenBy, ClosedBy: r.ClosedBy,
		})
		s.ResponseCount = int(r.ResponseCount)
		out = append(out, s)
	}
	return out, nil
}

// SosRespondedBy reports whether responderID responded to the SOS.
func (t *postgresTx) SosRespondedBy(ctx context.Context, sosID, responderID uuid.UUID) (bool, error) {
	ok, err := t.q.SosRespondedBy(ctx, emergencystore.SosRespondedByParams{SosID: sosID, ResponderID: responderID})
	if err != nil {
		return false, fmt.Errorf("sos responded by: %w", err)
	}
	return ok, nil
}

// ListSosFeed returns one page of a district feed.
func (t *postgresTx) ListSosFeed(ctx context.Context, f model.FeedFilter) ([]model.FeedRow, error) {
	district := f.DistrictID
	rows, err := t.q.ListSosFeed(ctx, emergencystore.ListSosFeedParams{
		ViewerID: f.ViewerID, DistrictID: &district, Now: f.Now,
		CursorCreatedAt: f.CursorCreatedAt, CursorID: f.CursorID, PageLimit: f.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list sos feed: %w", err)
	}
	out := make([]model.FeedRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.FeedRow{
			ID: r.ID, AuthorID: r.MemberID, Type: r.Type, Text: r.Content, CreatedAt: r.CreatedAt, RespondedByMe: r.RespondedByMe,
		})
	}
	return out, nil
}

// InsertSosResponse records "I can help"; inserted is false on a repeat.
func (t *postgresTx) InsertSosResponse(ctx context.Context, sosID, responderID uuid.UUID, at time.Time) (model.SosResponse, bool, error) {
	row, err := t.q.InsertSosResponse(ctx, emergencystore.InsertSosResponseParams{SosID: sosID, ResponderID: responderID, CreatedAt: at})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SosResponse{}, false, nil
	}
	if err != nil {
		return model.SosResponse{}, false, fmt.Errorf("insert sos response: %w", err)
	}
	return mapResponse(row), true, nil
}

// ListSosResponses returns the responses to an SOS, oldest first.
func (t *postgresTx) ListSosResponses(ctx context.Context, sosID uuid.UUID, limit int32) ([]model.SosResponse, error) {
	rows, err := t.q.ListSosResponses(ctx, emergencystore.ListSosResponsesParams{SosID: sosID, PageLimit: limit})
	if err != nil {
		return nil, fmt.Errorf("list sos responses: %w", err)
	}
	out := make([]model.SosResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapResponse(r))
	}
	return out, nil
}

// GetSosResponse returns the response, or model.ErrNotFound.
func (t *postgresTx) GetSosResponse(ctx context.Context, id uuid.UUID) (model.SosResponse, error) {
	row, err := t.q.GetSosResponse(ctx, id)
	if err != nil {
		return model.SosResponse{}, notFound(err, "get sos response")
	}
	return mapResponse(row), nil
}

// AcceptSosResponse marks the response accepted; updated is false when it was.
func (t *postgresTx) AcceptSosResponse(ctx context.Context, id uuid.UUID, at time.Time) (model.SosResponse, bool, error) {
	row, err := t.q.AcceptSosResponse(ctx, emergencystore.AcceptSosResponseParams{AcceptedAt: &at, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.SosResponse{}, false, nil
	}
	if err != nil {
		return model.SosResponse{}, false, fmt.Errorf("accept sos response: %w", err)
	}
	return mapResponse(row), true, nil
}

// UpsertChildCard stores the member's sealed child card.
func (t *postgresTx) UpsertChildCard(ctx context.Context, memberID uuid.UUID, sealed []byte, at time.Time) error {
	if err := t.q.UpsertChildCard(ctx, emergencystore.UpsertChildCardParams{MemberID: memberID, Content: sealed, UpdatedAt: at}); err != nil {
		return fmt.Errorf("upsert child card: %w", err)
	}
	return nil
}

// GetChildCard returns the member's child card, or model.ErrNotFound.
func (t *postgresTx) GetChildCard(ctx context.Context, memberID uuid.UUID) (model.ChildCard, error) {
	row, err := t.q.GetChildCard(ctx, memberID)
	if err != nil {
		return model.ChildCard{}, notFound(err, "get child card")
	}
	return model.ChildCard{MemberID: row.MemberID, Text: row.Content, UpdatedAt: row.UpdatedAt, LastSentAt: row.LastSentAt}, nil
}

// MarkChildCardSent records when the plan was last sent.
func (t *postgresTx) MarkChildCardSent(ctx context.Context, memberID uuid.UUID, at time.Time) error {
	if err := t.q.MarkChildCardSent(ctx, emergencystore.MarkChildCardSentParams{SentAt: &at, MemberID: memberID}); err != nil {
		return fmt.Errorf("mark child card sent: %w", err)
	}
	return nil
}

// InsertTrustedContact inserts a contact; inserted is false on any unique conflict.
func (t *postgresTx) InsertTrustedContact(ctx context.Context, c model.TrustedContact) (model.TrustedContact, bool, error) {
	row, err := t.q.InsertTrustedContact(ctx, emergencystore.InsertTrustedContactParams{
		ID: c.ID, OwnerID: c.OwnerID, IdempotencyKey: c.IdempotencyKey, Kind: c.Kind, ContactMemberID: c.ContactMemberID,
		Name: c.Name, Phone: c.Phone, Lang: c.Lang, Status: c.Status, CreatedAt: c.CreatedAt, ConfirmedAt: c.ConfirmedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.TrustedContact{}, false, nil
	}
	if err != nil {
		return model.TrustedContact{}, false, fmt.Errorf("insert trusted contact: %w", err)
	}
	return mapContact(row), true, nil
}

// GetTrustedContactByIdempotencyKey returns the owner's contact added with key, or model.ErrNotFound.
func (t *postgresTx) GetTrustedContactByIdempotencyKey(ctx context.Context, ownerID, key uuid.UUID) (model.TrustedContact, error) {
	row, err := t.q.GetTrustedContactByIdempotencyKey(ctx, emergencystore.GetTrustedContactByIdempotencyKeyParams{OwnerID: ownerID, IdempotencyKey: key})
	if err != nil {
		return model.TrustedContact{}, notFound(err, "get trusted contact by key")
	}
	return mapContact(row), nil
}

// GetTrustedContact returns the contact, or model.ErrNotFound.
func (t *postgresTx) GetTrustedContact(ctx context.Context, id uuid.UUID) (model.TrustedContact, error) {
	row, err := t.q.GetTrustedContact(ctx, id)
	if err != nil {
		return model.TrustedContact{}, notFound(err, "get trusted contact")
	}
	return mapContact(row), nil
}

// ListActiveTrustedContacts returns the owner's active contacts, oldest first.
func (t *postgresTx) ListActiveTrustedContacts(ctx context.Context, ownerID uuid.UUID) ([]model.TrustedContact, error) {
	rows, err := t.q.ListActiveTrustedContacts(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list trusted contacts: %w", err)
	}
	out := make([]model.TrustedContact, 0, len(rows))
	for _, r := range rows {
		out = append(out, mapContact(r))
	}
	return out, nil
}

// ConfirmTrustedContact confirms an active invitation of memberID.
func (t *postgresTx) ConfirmTrustedContact(ctx context.Context, id, memberID uuid.UUID, at time.Time) (model.TrustedContact, bool, error) {
	row, err := t.q.ConfirmTrustedContact(ctx, emergencystore.ConfirmTrustedContactParams{ConfirmedAt: &at, ID: id, MemberID: &memberID})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.TrustedContact{}, false, nil
	}
	if err != nil {
		return model.TrustedContact{}, false, fmt.Errorf("confirm trusted contact: %w", err)
	}
	return mapContact(row), true, nil
}

// RemoveTrustedContact marks the owner's active contact removed.
func (t *postgresTx) RemoveTrustedContact(ctx context.Context, id, ownerID uuid.UUID, at time.Time) (bool, error) {
	n, err := t.q.RemoveTrustedContact(ctx, emergencystore.RemoveTrustedContactParams{RemovedAt: &at, ID: id, OwnerID: ownerID})
	if err != nil {
		return false, fmt.Errorf("remove trusted contact: %w", err)
	}
	return n == 1, nil
}

// MarkContactsPlanSent records the send on the owner's confirmed contacts.
func (t *postgresTx) MarkContactsPlanSent(ctx context.Context, ownerID uuid.UUID, at time.Time) (int64, error) {
	n, err := t.q.MarkContactsPlanSent(ctx, emergencystore.MarkContactsPlanSentParams{SentAt: &at, OwnerID: ownerID})
	if err != nil {
		return 0, fmt.Errorf("mark contacts plan sent: %w", err)
	}
	return n, nil
}

// GetSharedPlanContact returns the confirmed member contact of ownerID that is memberID.
func (t *postgresTx) GetSharedPlanContact(ctx context.Context, ownerID, memberID uuid.UUID) (model.TrustedContact, error) {
	row, err := t.q.GetSharedPlanContact(ctx, emergencystore.GetSharedPlanContactParams{OwnerID: ownerID, MemberID: &memberID})
	if err != nil {
		return model.TrustedContact{}, notFound(err, "get shared plan contact")
	}
	return mapContact(row), nil
}

// InsertPlanSend records a plan send; inserted is false on a repeated key.
func (t *postgresTx) InsertPlanSend(ctx context.Context, p model.PlanSend) (model.PlanSend, bool, error) {
	row, err := t.q.InsertPlanSend(ctx, emergencystore.InsertPlanSendParams{
		ID: p.ID, MemberID: p.MemberID, IdempotencyKey: p.IdempotencyKey,
		ContactCount: int32(p.ContactCount), //nolint:gosec // at most model.MaxTrustedContacts
		SentAt:       p.SentAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.PlanSend{}, false, nil
	}
	if err != nil {
		return model.PlanSend{}, false, fmt.Errorf("insert plan send: %w", err)
	}
	return mapPlanSend(row), true, nil
}

// GetPlanSendByIdempotencyKey returns the member's send made with key, or model.ErrNotFound.
func (t *postgresTx) GetPlanSendByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.PlanSend, error) {
	row, err := t.q.GetPlanSendByIdempotencyKey(ctx, emergencystore.GetPlanSendByIdempotencyKeyParams{MemberID: memberID, IdempotencyKey: key})
	if err != nil {
		return model.PlanSend{}, notFound(err, "get plan send by key")
	}
	return mapPlanSend(row), nil
}

// CountPlanSendsSince counts the member's plan sends after since.
func (t *postgresTx) CountPlanSendsSince(ctx context.Context, memberID uuid.UUID, since time.Time) (int, error) {
	n, err := t.q.CountPlanSendsSince(ctx, emergencystore.CountPlanSendsSinceParams{MemberID: memberID, Since: since})
	if err != nil {
		return 0, fmt.Errorf("count plan sends: %w", err)
	}
	return int(n), nil
}

// Publish enqueues e in this transaction's outbox.
func (t *postgresTx) Publish(ctx context.Context, now time.Time, e model.Event) error {
	_, err := outbox.Enqueue(ctx, t.platform, now, "", outbox.Event{Type: e.Type, Version: e.Version, Data: e.Data})
	return err
}

// ScheduleAutoClose schedules emergency.SosAutoClose (dedup key = SOS id).
func (t *postgresTx) ScheduleAutoClose(ctx context.Context, sosID uuid.UUID, dueAt time.Time) (bool, error) {
	return timers.Schedule(ctx, t.platform, timers.Timer{
		Kind:      model.TimerSosAutoClose,
		SubjectID: sosID,
		DedupKey:  sosID.String(),
		DueAt:     dueAt,
		Payload:   map[string]string{},
	})
}

// CancelAutoClose cancels the pending auto-close timer of the SOS.
func (t *postgresTx) CancelAutoClose(ctx context.Context, sosID uuid.UUID, at time.Time) (int64, error) {
	n, err := timers.Cancel(ctx, t.platform, at, model.TimerSosAutoClose, sosID)
	if err != nil {
		return 0, fmt.Errorf("cancel auto-close timer: %w", err)
	}
	return n, nil
}

func mapSos(r emergencystore.EmergencySosRequest) model.Sos {
	return model.Sos{
		ID: r.ID, MemberID: r.MemberID, IdempotencyKey: r.IdempotencyKey, Type: r.Type, Text: r.Content,
		Status: r.Status, CreatedAt: r.CreatedAt, ClosesAt: r.ClosesAt, DistrictID: r.DistrictID,
		SharedAt: r.SharedAt, ClosedAt: r.ClosedAt, CloseReason: r.CloseReason,
		SeenAt: r.SeenAt, SeenBy: r.SeenBy, ClosedBy: r.ClosedBy,
	}
}

func mapResponse(r emergencystore.EmergencySosResponse) model.SosResponse {
	return model.SosResponse{ID: r.ID, SosID: r.SosID, ResponderID: r.ResponderID, CreatedAt: r.CreatedAt, AcceptedAt: r.AcceptedAt}
}

func mapContact(r emergencystore.EmergencyTrustedContact) model.TrustedContact {
	return model.TrustedContact{
		ID: r.ID, OwnerID: r.OwnerID, IdempotencyKey: r.IdempotencyKey, Kind: r.Kind, ContactMemberID: r.ContactMemberID,
		Name: r.Name, Phone: r.Phone, Lang: r.Lang, Status: r.Status, CreatedAt: r.CreatedAt,
		ConfirmedAt: r.ConfirmedAt, PlanSentAt: r.PlanSentAt, RemovedAt: r.RemovedAt,
	}
}

func mapPlanSend(r emergencystore.EmergencyPlanSend) model.PlanSend {
	return model.PlanSend{ID: r.ID, MemberID: r.MemberID, IdempotencyKey: r.IdempotencyKey, ContactCount: int(r.ContactCount), SentAt: r.SentAt}
}
