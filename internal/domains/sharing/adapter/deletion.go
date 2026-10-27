package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/sharing/model"
	"birge/internal/domains/sharing/port"
	"birge/internal/domains/sharing/service"
	"birge/internal/domains/sharing/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. Listings and uploads are locked
// and their object keys collected before the rows go; the object-deletion
// timer is scheduled only when there are keys, so a repeated run (no rows
// left) schedules nothing.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q, platform := store.New(tx), platformstore.New(tx)
		listings, err := q.ListMemberListingsForDeletion(ctx, memberID)
		if err != nil {
			return fmt.Errorf("sharing: list member listings: %w", err)
		}
		uploads, err := q.ListMemberUploadsForDeletion(ctx, memberID)
		if err != nil {
			return fmt.Errorf("sharing: list member uploads: %w", err)
		}
		listingIDs := make([]uuid.UUID, 0, len(listings))
		var keys []string
		for _, l := range listings {
			listingIDs = append(listingIDs, l.ID)
			if l.PhotoObjectKey != nil {
				keys = append(keys, *l.PhotoObjectKey)
			}
		}
		for _, u := range uploads {
			keys = append(keys, u.ObjectKey)
			if u.ProcessedObjectKey != nil {
				keys = append(keys, *u.ProcessedObjectKey)
			}
		}
		slices.Sort(keys)
		keys = slices.Compact(keys)

		if _, err := timers.CancelSubjects(ctx, platform, now,
			[]string{model.TimerProcessPhoto, model.TimerAutoClose}, listingIDs); err != nil {
			return err
		}
		if _, err := q.DeleteMemberContactRequests(ctx, store.DeleteMemberContactRequestsParams{
			MemberID: memberID, ListingIds: listingIDs,
		}); err != nil {
			return fmt.Errorf("sharing: delete contact requests: %w", err)
		}
		if _, err := q.DeleteMemberListings(ctx, memberID); err != nil {
			return fmt.Errorf("sharing: delete listings: %w", err)
		}
		if _, err := q.DeleteMemberUploads(ctx, memberID); err != nil {
			return fmt.Errorf("sharing: delete photo uploads: %w", err)
		}
		if len(keys) > 0 {
			// No dedup key: a later run never has keys (the rows are gone),
			// and two timers for the same member would each delete their own keys.
			if _, err := timers.Schedule(ctx, platform, timers.Timer{
				Kind: model.TimerDeleteObjects, SubjectID: memberID, DueAt: now,
				Payload: model.DeleteObjectsPayload{Keys: keys},
			}); err != nil {
				return err
			}
		}
		return audit.RecordMemberDeleted(ctx, platform, "sharing", memberID)
	})
}

// DeletionTimerHandlers adapts the account-deletion timer to service.Deletion.
type DeletionTimerHandlers struct {
	Deletion *service.Deletion
}

// Handlers returns the kind -> handler map for timers.Worker.
func (h *DeletionTimerHandlers) Handlers() map[string]timers.Handler {
	return map[string]timers.Handler{model.TimerDeleteObjects: h.DeleteObjects}
}

// DeleteObjects handles sharing.DeleteObjects: it deletes every key of the
// payload. Object I/O runs outside any table change; a failure reschedules
// the timer and the retry deletes the keys again (a missing key is not an
// error for the object store).
func (h *DeletionTimerHandlers) DeleteObjects(ctx context.Context, _ pgx.Tx, due timers.Due) error {
	var p model.DeleteObjectsPayload
	if err := json.Unmarshal(due.Payload, &p); err != nil {
		return fmt.Errorf("sharing: delete objects payload: %w", err)
	}
	return h.Deletion.DeleteObjects(ctx, p.Keys)
}
