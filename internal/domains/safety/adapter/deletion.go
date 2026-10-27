package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/safety/port"
	"birge/internal/domains/safety/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. Timers are left alone: the
// backup timer's subject is the escalation, which stays.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, _ time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := store.New(tx).ClearMemberEscalations(ctx, memberID); err != nil {
			return fmt.Errorf("safety: clear escalations: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platformstore.New(tx), "safety", memberID)
	})
}
