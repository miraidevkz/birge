// Package audit appends rows to platform.audit_log (append-only; see
// db/roles.sql). Callers pass a Queries bound to their transaction so the
// entry commits or rolls back with the change it records.
//
// Reason never carries free text or personal data: only a short code, or
// empty when there is nothing to say.
package audit

import (
	"context"
	"fmt"
	"regexp"

	"github.com/google/uuid"

	"birge/internal/platform/store"
)

// codeRe is the shape of Action, ObjectType and a non-empty Reason: a short
// identifier such as "escalation.take" or "safety.escalation", never prose.
var codeRe = regexp.MustCompile(`^[a-z][a-z_.]{1,63}$`)

// Entry is one audit_log row. OccurredAt is set by the database (DEFAULT now()).
type Entry struct {
	ActorID    uuid.UUID
	ActorRole  string
	Action     string
	ObjectType string
	ObjectID   uuid.UUID // zero means NULL
	Reason     string    // short code only; empty when unused
}

// Record validates e and inserts it using q, which must be bound to the
// caller's transaction (store.New(tx)).
func Record(ctx context.Context, q *store.Queries, e Entry) error {
	if err := validate(e); err != nil {
		return err
	}
	var objectID *uuid.UUID
	if e.ObjectID != uuid.Nil {
		objectID = &e.ObjectID
	}
	var reason *string
	if e.Reason != "" {
		reason = &e.Reason
	}
	if err := q.InsertAuditEntry(ctx, store.InsertAuditEntryParams{
		ActorID:    e.ActorID,
		ActorRole:  e.ActorRole,
		Action:     e.Action,
		ObjectType: e.ObjectType,
		ObjectID:   objectID,
		Reason:     reason,
	}); err != nil {
		return fmt.Errorf("audit: insert %s: %w", e.Action, err)
	}
	return nil
}

// SystemActorID is the actor of entries written by the platform itself
// (account deletion), not by a person. It is a fixed, documented value so a
// reader can tell such entries apart from those of staff and members.
var SystemActorID = uuid.MustParse("00000000-0000-7000-8000-000000000001")

// RoleSystem is the actor role of entries written with SystemActorID.
const RoleSystem = "system"

// AuditObjectMember is the object type of entries about a member as a whole.
const AuditObjectMember = "member"

// RecordOnce inserts e unless an entry with the same action, object type and
// object ID exists. Idempotent work (account deletion re-run after a
// redelivered event) uses it so a retry adds no second entry. e.ObjectID is
// required. Two concurrent first calls could both insert; callers that must
// not race lock their own rows first.
func RecordOnce(ctx context.Context, q *store.Queries, e Entry) error {
	if e.ObjectID == uuid.Nil {
		return fmt.Errorf("audit: object id is required for %s", e.Action)
	}
	if err := validate(e); err != nil {
		return err
	}
	found, err := q.AuditEntryExists(ctx, store.AuditEntryExistsParams{
		ObjectType: e.ObjectType, ObjectID: e.ObjectID, Action: e.Action,
	})
	if err != nil {
		return fmt.Errorf("audit: look up %s: %w", e.Action, err)
	}
	if found {
		return nil
	}
	return Record(ctx, q, e)
}

// MemberDeleted is the entry a bounded context writes once when it has
// erased a member's data on account deletion: action
// "<context>.member_deleted", object "member" with the member ID, actor system.
func MemberDeleted(boundedContext string, memberID uuid.UUID) Entry {
	return Entry{
		ActorID: SystemActorID, ActorRole: RoleSystem,
		Action: boundedContext + ".member_deleted", ObjectType: AuditObjectMember, ObjectID: memberID,
	}
}

// RecordMemberDeleted writes MemberDeleted(boundedContext, memberID) once.
func RecordMemberDeleted(ctx context.Context, q *store.Queries, boundedContext string, memberID uuid.UUID) error {
	return RecordOnce(ctx, q, MemberDeleted(boundedContext, memberID))
}

func validate(e Entry) error {
	if e.ActorID == uuid.Nil {
		return fmt.Errorf("audit: actor id is required")
	}
	if e.ActorRole == "" {
		return fmt.Errorf("audit: actor role is required")
	}
	if !codeRe.MatchString(e.Action) {
		return fmt.Errorf("audit: invalid action %q", e.Action)
	}
	if !codeRe.MatchString(e.ObjectType) {
		return fmt.Errorf("audit: invalid object type %q", e.ObjectType)
	}
	if e.Reason != "" && !codeRe.MatchString(e.Reason) {
		return fmt.Errorf("audit: invalid reason %q (short code only, no free text)", e.Reason)
	}
	return nil
}
