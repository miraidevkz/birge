// Package port defines the interfaces the emergency service needs: its store
// (SOS requests, responses, child cards, trusted contacts, plan sends,
// external plan and SOS links, outbox, timers, audit), field sealing, and what it
// reads from other contexts (member profiles and blocks from membership, "met in person"
// from the circle). Adapters for the other contexts are wired later; tests
// use the fakes in emergencytest.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/emergency/model"
)

// Sealer encrypts and decrypts one member's fields with that member's data
// key (platform crypto, ADR-0009). Everything emergency seals belongs to the
// owner: the SOS text to its author, the child card and the external
// contacts' names and phones to the plan's owner, so her account deletion
// makes all of it unreadable.
type Sealer interface {
	Seal(ctx context.Context, memberID uuid.UUID, plaintext, associatedData []byte) ([]byte, error)
	Open(ctx context.Context, memberID uuid.UUID, ciphertext, associatedData []byte) ([]byte, error)
}

// MemberInfo reads member profiles owned by membership. Results are never
// logged, stored or put into events.
type MemberInfo interface {
	// Profiles returns the profiles of the given members; unknown or deleted
	// members are absent from the map.
	Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]model.Profile, error)
}

// Blocks reads member blocks owned by membership (ADR-0019).
type Blocks interface {
	// IsBlocked reports whether a blocked b or b blocked a.
	IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Acquaintances reads the "met in person" relation of a pair of members
// (§ 3: a property of the pair). Implemented later by the circle context.
type Acquaintances interface {
	// Met reports whether a and b both confirmed they met in person.
	Met(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// Auditor appends to platform.audit_log in the current transaction. Entries
// carry IDs and fixed codes only.
type Auditor interface {
	Audit(ctx context.Context, e model.AuditEntry) error
}

// Store is the persistence port for emergency tables and platform hooks.
type Store interface {
	// InTx runs fn in one database transaction: committed when fn returns
	// nil, rolled back otherwise.
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction. Every write a use case makes
// (rows, outbox events, timers, audit) goes through the same Tx so they
// commit or roll back together.
type Tx interface {
	Auditor

	// Lock takes a transaction-scoped lock on (scope, memberID), so limit
	// checks (count, then insert) cannot race.
	Lock(ctx context.Context, scope string, memberID uuid.UUID) error

	// CountSosSince counts the member's SOS requests created after since.
	CountSosSince(ctx context.Context, memberID uuid.UUID, since time.Time) (int, error)
	// InsertSos inserts an open SOS whose text is already sealed; inserted
	// is false when the member already used s.IdempotencyKey.
	InsertSos(ctx context.Context, s model.Sos) (created model.Sos, inserted bool, err error)
	// GetSosByIdempotencyKey returns the member's SOS raised with key, or model.ErrNotFound.
	GetSosByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.Sos, error)
	// GetSos returns the SOS with its response count, or model.ErrNotFound.
	// With forUpdate the row stays locked until the transaction ends.
	GetSos(ctx context.Context, id uuid.UUID, forUpdate bool) (model.Sos, error)
	// ListMySos returns the member's latest SOS requests with response counts, newest first.
	ListMySos(ctx context.Context, memberID uuid.UUID, limit int32) ([]model.Sos, error)
	// ShareSos shares an open, unshared, non-childcare SOS to districtID;
	// updated is false otherwise.
	ShareSos(ctx context.Context, id, districtID uuid.UUID, at time.Time) (s model.Sos, updated bool, err error)
	// CloseSos closes an open SOS; updated is false when it was closed
	// already. closedBy is the staff ID of a moderator's close, nil for the
	// author and the timer.
	CloseSos(ctx context.Context, id uuid.UUID, reason string, closedBy *uuid.UUID, at time.Time) (s model.Sos, updated bool, err error)
	// MarkSosSeen records the first moderator's "Seen"; updated is false
	// when the SOS was seen already (the first staff ID and time are kept).
	MarkSosSeen(ctx context.Context, id, staffID uuid.UUID, at time.Time) (s model.Sos, updated bool, err error)
	// ListStaffSos returns one page of the moderator queue with response
	// counts: open oldest first, closed newest first.
	ListStaffSos(ctx context.Context, f model.StaffSosFilter) ([]model.Sos, error)
	// ListSosFeed returns one page of a district feed: open, shared, never
	// childcare, never the viewer's own, newest first.
	ListSosFeed(ctx context.Context, f model.FeedFilter) ([]model.FeedRow, error)

	// InsertSosResponse records "I can help"; inserted is false when the
	// responder already responded.
	InsertSosResponse(ctx context.Context, sosID, responderID uuid.UUID, at time.Time) (created model.SosResponse, inserted bool, err error)
	// SosRespondedBy reports whether responderID already responded to the SOS.
	SosRespondedBy(ctx context.Context, sosID, responderID uuid.UUID) (bool, error)
	// ListSosResponses returns the responses to an SOS, oldest first.
	ListSosResponses(ctx context.Context, sosID uuid.UUID, limit int32) ([]model.SosResponse, error)
	// GetSosResponse returns the response, or model.ErrNotFound.
	GetSosResponse(ctx context.Context, id uuid.UUID) (model.SosResponse, error)
	// AcceptSosResponse marks the response accepted; updated is false when
	// it was accepted already.
	AcceptSosResponse(ctx context.Context, id uuid.UUID, at time.Time) (r model.SosResponse, updated bool, err error)

	// UpsertChildCard stores the member's sealed child card.
	UpsertChildCard(ctx context.Context, memberID uuid.UUID, sealed []byte, at time.Time) error
	// GetChildCard returns the member's child card, or model.ErrNotFound.
	GetChildCard(ctx context.Context, memberID uuid.UUID) (model.ChildCard, error)
	// MarkChildCardSent records when the plan was last sent.
	MarkChildCardSent(ctx context.Context, memberID uuid.UUID, at time.Time) error

	// InsertTrustedContact inserts a contact whose name and phone are
	// already sealed; inserted is false on a repeated idempotency key or
	// when the member is already an active contact of the owner.
	InsertTrustedContact(ctx context.Context, c model.TrustedContact) (created model.TrustedContact, inserted bool, err error)
	// GetTrustedContactByIdempotencyKey returns the owner's contact added with key, or model.ErrNotFound.
	GetTrustedContactByIdempotencyKey(ctx context.Context, ownerID, key uuid.UUID) (model.TrustedContact, error)
	// GetTrustedContact returns the contact (also a removed one), or model.ErrNotFound.
	GetTrustedContact(ctx context.Context, id uuid.UUID) (model.TrustedContact, error)
	// ListActiveTrustedContacts returns the owner's invited and confirmed contacts, oldest first.
	ListActiveTrustedContacts(ctx context.Context, ownerID uuid.UUID) ([]model.TrustedContact, error)
	// ConfirmTrustedContact confirms an active invitation of memberID;
	// updated is false otherwise.
	ConfirmTrustedContact(ctx context.Context, id, memberID uuid.UUID, at time.Time) (c model.TrustedContact, updated bool, err error)
	// RemoveTrustedContact marks the owner's active contact removed; removed
	// is false when there was none (the row is kept).
	RemoveTrustedContact(ctx context.Context, id, ownerID uuid.UUID, at time.Time) (removed bool, err error)
	// MarkContactsPlanSent records the send on the owner's confirmed contacts.
	MarkContactsPlanSent(ctx context.Context, ownerID uuid.UUID, at time.Time) (int64, error)
	// GetSharedPlanContact returns the active, confirmed member contact of
	// ownerID that is memberID, or model.ErrNotFound. It is the access check
	// of everything a trusted contact may read (shared plan, trusted SOS).
	GetSharedPlanContact(ctx context.Context, ownerID, memberID uuid.UUID) (model.TrustedContact, error)

	// InsertPlanSend records a plan send; inserted is false on a repeated key.
	InsertPlanSend(ctx context.Context, p model.PlanSend) (created model.PlanSend, inserted bool, err error)
	// GetPlanSendByIdempotencyKey returns the member's send made with key, or model.ErrNotFound.
	GetPlanSendByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (model.PlanSend, error)
	// CountPlanSendsSince counts the member's plan sends after since.
	CountPlanSendsSince(ctx context.Context, memberID uuid.UUID, since time.Time) (int, error)

	// InsertPlanLink stores a new external plan link (keyed hash only).
	InsertPlanLink(ctx context.Context, l model.PlanLink) error
	// RevokeContactPlanLinks revokes the contact's links that are not
	// revoked yet; returns how many.
	RevokeContactPlanLinks(ctx context.Context, contactID uuid.UUID, at time.Time) (int64, error)
	// RevokeOwnerPlanLinks revokes every unrevoked link of the owner's contacts.
	RevokeOwnerPlanLinks(ctx context.Context, ownerID uuid.UUID, at time.Time) (int64, error)
	// GetLivePlanLink returns the link with tokenHash that is neither revoked
	// nor expired at now, or model.ErrNotFound for every other case (one
	// lookup, so the failures cannot be told apart).
	GetLivePlanLink(ctx context.Context, tokenHash []byte, now time.Time) (model.PlanLink, error)
	// DeleteExpiredPlanLink deletes the link when it expired at or before
	// cutoff; returns how many rows went (0 or 1).
	DeleteExpiredPlanLink(ctx context.Context, linkID uuid.UUID, cutoff time.Time) (int64, error)
	// SchedulePlanLinkPurge schedules emergency.PlanLinkPurge for the link
	// (dedup key = link id).
	SchedulePlanLinkPurge(ctx context.Context, linkID uuid.UUID, dueAt time.Time) (bool, error)

	// InsertSosLink stores a new external SOS link (keyed hash only).
	InsertSosLink(ctx context.Context, l model.SosLink) error
	// RevokeSosContactLinks revokes the unrevoked links of this SOS and
	// contact; returns how many.
	RevokeSosContactLinks(ctx context.Context, sosID, contactID uuid.UUID, at time.Time) (int64, error)
	// GetLiveSosLink returns the link with tokenHash and its SOS when the
	// link is neither revoked nor expired, the SOS is open and not past
	// closes_at, and the contact is active, external, confirmed and the
	// author's; model.ErrNotFound for every other case (one lookup, so the
	// failures cannot be told apart).
	GetLiveSosLink(ctx context.Context, tokenHash []byte, now time.Time) (model.LiveSosLink, error)
	// DeleteExpiredSosLink deletes the link when it expired at or before
	// cutoff; returns how many rows went (0 or 1).
	DeleteExpiredSosLink(ctx context.Context, linkID uuid.UUID, cutoff time.Time) (int64, error)
	// ScheduleSosLinkPurge schedules emergency.SosLinkPurge for the link
	// (dedup key = link id).
	ScheduleSosLinkPurge(ctx context.Context, linkID uuid.UUID, dueAt time.Time) (bool, error)

	// RecentStaffSosViews returns the SOS among sosIDs that staffID already
	// has an emergency_sos.view_staff audit entry for within the last
	// within (on the audit log's clock), in one lookup.
	RecentStaffSosViews(ctx context.Context, staffID uuid.UUID, sosIDs []uuid.UUID, within time.Duration) (map[uuid.UUID]bool, error)

	// Publish enqueues e in this transaction's outbox (ADR-0007).
	Publish(ctx context.Context, now time.Time, e model.Event) error
	// ScheduleAutoClose schedules emergency.SosAutoClose for the SOS (dedup
	// key = SOS id); scheduled is false on a dedup hit.
	ScheduleAutoClose(ctx context.Context, sosID uuid.UUID, dueAt time.Time) (scheduled bool, err error)
	// CancelAutoClose cancels the pending auto-close timer; returns how many were cancelled.
	CancelAutoClose(ctx context.Context, sosID uuid.UUID, at time.Time) (int64, error)
}
