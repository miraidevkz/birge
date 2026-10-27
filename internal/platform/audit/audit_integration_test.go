package audit_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/platform/audit"
	"birge/internal/platform/db"
	"birge/internal/platform/store"
	"birge/internal/platform/testinfra"
)

func TestRecordCommitsAndRollsBackWithCallerTx(t *testing.T) {
	ctx := context.Background()
	pool := testinfra.Postgres(t)
	actor := uuid.MustParse("01890a5d-ac96-774b-bcce-b302099a8057")
	object := uuid.MustParse("01890a5d-ac96-774b-bcce-b302099a8058")
	entry := audit.Entry{
		ActorID:    actor,
		ActorRole:  "member",
		Action:     "emergency_plan.view_shared",
		ObjectType: "emergency.child_card",
		ObjectID:   object,
		Reason:     "plan.sent",
	}

	if err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		return audit.Record(ctx, store.New(tx), entry)
	}); err != nil {
		t.Fatal(err)
	}
	if got := countAudit(t, pool, entry.Action, object); got != 1 {
		t.Fatalf("committed rows = %d, want 1", got)
	}

	_ = db.InTx(ctx, pool, func(tx pgx.Tx) error {
		if err := audit.Record(ctx, store.New(tx), entry); err != nil {
			t.Fatal(err)
		}
		if got := countAuditTx(t, ctx, tx, entry.Action, object); got != 2 {
			t.Fatalf("in-tx rows = %d, want 2", got)
		}
		return context.Canceled
	})
	if got := countAudit(t, pool, entry.Action, object); got != 1 {
		t.Fatalf("after rollback rows = %d, want 1", got)
	}
}

func countAudit(t *testing.T, pool *pgxpool.Pool, action string, objectID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM platform.audit_log WHERE action = $1 AND object_id = $2`,
		action, objectID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countAuditTx(t *testing.T, ctx context.Context, tx pgx.Tx, action string, objectID uuid.UUID) int {
	t.Helper()
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM platform.audit_log WHERE action = $1 AND object_id = $2`,
		action, objectID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
