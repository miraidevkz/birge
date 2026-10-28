package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/emergency/model"
	"birge/internal/domains/emergency/port"
	"birge/internal/domains/emergency/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
	"birge/internal/platform/timers"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. Her SOS links go first; then
// her requests are listed: responses reference them (foreign key) and their timers are
// cancelled by request ID.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, now time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q, platform := store.New(tx), platformstore.New(tx)
		// SOS links reference her requests and her contacts (foreign
		// keys): they go first, with their purge timers. A link's owner is
		// always the SOS author, so one owner query finds all of them.
		sosLinkIDs, err := q.ListOwnerSosLinkIDs(ctx, memberID)
		if err != nil {
			return fmt.Errorf("emergency: list sos links: %w", err)
		}
		if _, err := timers.CancelSubjects(ctx, platform, now, []string{model.TimerSosLinkPurge}, sosLinkIDs); err != nil {
			return err
		}
		if _, err := q.DeleteOwnerSosLinks(ctx, memberID); err != nil {
			return fmt.Errorf("emergency: delete sos links: %w", err)
		}
		sosIDs, err := q.ListMemberSosIDs(ctx, memberID)
		if err != nil {
			return fmt.Errorf("emergency: list member sos: %w", err)
		}
		if _, err := timers.CancelSubjects(ctx, platform, now, []string{model.TimerSosAutoClose}, sosIDs); err != nil {
			return err
		}
		if sosIDs == nil {
			sosIDs = []uuid.UUID{}
		}
		if _, err := q.DeleteMemberSosResponses(ctx, store.DeleteMemberSosResponsesParams{SosIds: sosIDs, MemberID: memberID}); err != nil {
			return fmt.Errorf("emergency: delete sos responses: %w", err)
		}
		if _, err := q.DeleteMemberSos(ctx, memberID); err != nil {
			return fmt.Errorf("emergency: delete sos: %w", err)
		}
		if _, err := q.DeleteMemberChildCard(ctx, memberID); err != nil {
			return fmt.Errorf("emergency: delete child card: %w", err)
		}
		// Links reference her contacts (foreign key): they go first, with
		// their purge timers.
		linkIDs, err := q.ListOwnerPlanLinkIDs(ctx, memberID)
		if err != nil {
			return fmt.Errorf("emergency: list plan links: %w", err)
		}
		if _, err := timers.CancelSubjects(ctx, platform, now, []string{model.TimerPlanLinkPurge}, linkIDs); err != nil {
			return err
		}
		if _, err := q.DeleteOwnerPlanLinks(ctx, memberID); err != nil {
			return fmt.Errorf("emergency: delete plan links: %w", err)
		}
		if _, err := q.DeleteMemberTrustedContacts(ctx, memberID); err != nil {
			return fmt.Errorf("emergency: delete trusted contacts: %w", err)
		}
		if _, err := q.DeleteMemberPlanSends(ctx, memberID); err != nil {
			return fmt.Errorf("emergency: delete plan sends: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platform, "emergency", memberID)
	})
}
