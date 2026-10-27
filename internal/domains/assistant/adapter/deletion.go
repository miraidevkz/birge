package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/assistant/port"
	"birge/internal/domains/assistant/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. The daily retention timer is
// global (not hers) and keeps running.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, _ time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := store.New(tx).DeleteMemberMessages(ctx, memberID); err != nil {
			return fmt.Errorf("assistant: delete messages: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platformstore.New(tx), "assistant", memberID)
	})
}
