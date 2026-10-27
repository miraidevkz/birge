package adapter_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/directory/adapter"
	"birge/internal/domains/directory/directorytest"
	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/service"
	"birge/internal/platform/auth"
	"birge/internal/platform/testinfra"
)

type env struct {
	pool    *pgxpool.Pool
	svc     *service.Service
	members *directorytest.FakeMembers
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testinfra.Postgres(t)
	members := directorytest.NewFakeMembers()
	return &env{pool: pool, members: members, svc: &service.Service{
		Store: &adapter.PostgresStore{Pool: pool}, Members: members,
		Clock:  &directorytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

func (e *env) member(verified bool, name string) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{Verified: verified, DisplayName: name})
	return p
}

func TestSpecialistReviewRatingsPostgres(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	adder := e.member(true, "Aigul Example")
	r1 := e.member(true, "Dana Example")
	r2 := e.member(true, "Sara Example")

	sp, err := e.svc.SuggestSpecialist(ctx, adder, uuid.Must(uuid.NewV7()), model.NewSpecialist{
		Name: "Example Clinic", Profession: "speech_therapist", DistrictID: uuid.New(),
		Needs: []string{"autism"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindSpecialist, ObjectID: sp.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}

	rev, err := e.svc.PutReview(ctx, r1, sp.ID, model.NewReview{Rating: 5, Text: "Example review"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindReview, ObjectID: rev.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	rev2, err := e.svc.PutReview(ctx, r1, sp.ID, model.NewReview{Rating: 2, Text: "Example review updated"})
	if err != nil {
		t.Fatal(err)
	}
	if rev2.ID != rev.ID {
		t.Fatal("id changed on upsert")
	}
	var status string
	var rating int
	if err := e.pool.QueryRow(ctx, `SELECT status, rating FROM directory.reviews WHERE id=$1`, rev.ID).
		Scan(&status, &rating); err != nil {
		t.Fatal(err)
	}
	if status != model.StatusPending || rating != 2 {
		t.Fatalf("status=%s rating=%d", status, rating)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindReview, ObjectID: rev.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	revB, err := e.svc.PutReview(ctx, r2, sp.ID, model.NewReview{Rating: 4, Text: "Example other"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindReview, ObjectID: revB.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}

	page, err := e.svc.SearchSpecialists(ctx, adder, nil, nil, nil, "")
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	item := page.Items[0]
	if item.ReviewCount != 2 || item.AverageRating == nil || *item.AverageRating != 3 {
		t.Fatalf("avg=%v count=%d", item.AverageRating, item.ReviewCount)
	}
	if len(item.LatestReviews) != 2 {
		t.Fatalf("latest %d", len(item.LatestReviews))
	}
	for _, lr := range item.LatestReviews {
		if lr.AuthorName == "" || len(lr.AuthorName) > 20 {
			t.Fatalf("authorName %q", lr.AuthorName)
		}
	}
}
