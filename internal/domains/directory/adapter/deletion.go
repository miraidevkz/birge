package adapter

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"birge/internal/domains/directory/port"
	"birge/internal/domains/directory/store"
	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	platformstore "birge/internal/platform/store"
)

var _ port.MemberEraser = (*PostgresStore)(nil)

// EraseMember implements port.MemberEraser. Reviews go first: a card she
// suggested and reviewed herself is then free to delete.
func (s *PostgresStore) EraseMember(ctx context.Context, memberID uuid.UUID, _ time.Time) error {
	return db.InTx(ctx, s.Pool, func(tx pgx.Tx) error {
		q := store.New(tx)
		if _, err := q.DeleteMemberReviews(ctx, memberID); err != nil {
			return fmt.Errorf("directory: delete reviews: %w", err)
		}
		if _, err := q.DeleteMemberUnusedSpecialists(ctx, memberID); err != nil {
			return fmt.Errorf("directory: delete specialists: %w", err)
		}
		return audit.RecordMemberDeleted(ctx, platformstore.New(tx), "directory", memberID)
	})
}
