package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/directory/directorytest"
	"birge/internal/domains/directory/model"
	"birge/internal/domains/directory/service"
	"birge/internal/platform/auth"
)

const (
	fakeName = "Example Clinic"
	fakeText = "Example review text"
)

type env struct {
	svc     *service.Service
	store   *directorytest.MemoryStore
	members *directorytest.FakeMembers
	logs    *bytes.Buffer
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := directorytest.NewMemoryStore()
	members := directorytest.NewFakeMembers()
	var logs bytes.Buffer
	e := &env{store: store, members: members, logs: &logs}
	e.svc = &service.Service{
		Store: store, Members: members,
		Clock:  &directorytest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	t.Cleanup(func() {
		for _, bad := range []string{fakeName, fakeText, "Example"} {
			if strings.Contains(logs.String(), bad) {
				t.Errorf("log leaked %q", bad)
			}
		}
		for _, ev := range store.EventsOfType(model.EventItemSubmitted) {
			raw, _ := json.Marshal(ev.Data)
			for _, bad := range []string{fakeName, fakeText} {
				if strings.Contains(string(raw), bad) {
					t.Errorf("event leaked %q", bad)
				}
			}
		}
	})
	return e
}

func member(e *env, verified bool, name string) auth.Principal {
	p := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()}
	e.members.Put(p.MemberID, model.Profile{Verified: verified, DisplayName: name})
	return p
}

func TestSuggestAndModerateSpecialist(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true, "Aigul Example")
	key := uuid.Must(uuid.NewV7())
	in := model.NewSpecialist{
		Name: fakeName, Profession: "speech_therapist", DistrictID: uuid.New(),
		Needs: []string{"autism"},
	}
	v, err := e.svc.SuggestSpecialist(ctx, author, key, in)
	if err != nil || v.Status != model.StatusPending {
		t.Fatalf("%+v %v", v, err)
	}
	if len(e.store.EventsOfType(model.EventItemSubmitted)) != 1 {
		t.Fatal("expected ItemSubmitted")
	}
	v2, err := e.svc.SuggestSpecialist(ctx, author, key, in)
	if err != nil || v2.ID != v.ID {
		t.Fatalf("idempotent %+v %v", v2, err)
	}
	if _, err := e.svc.SuggestSpecialist(ctx, member(e, false, "X"), uuid.New(), in); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unverified: %v", err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindSpecialist, ObjectID: v.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	sp, _ := e.store.Specialist(v.ID)
	if sp.Status != model.StatusActive {
		t.Fatalf("status %s", sp.Status)
	}
}

func TestOneReviewPerMemberAndRatings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	adder := member(e, true, "Aigul Example")
	r1 := member(e, true, "Dana Example")
	r2 := member(e, true, "Sara Example")
	sp, err := e.svc.SuggestSpecialist(ctx, adder, uuid.Must(uuid.NewV7()), model.NewSpecialist{
		Name: fakeName, Profession: "psychologist", DistrictID: uuid.New(), Needs: []string{"autism"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindSpecialist, ObjectID: sp.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}

	rev, err := e.svc.PutReview(ctx, r1, sp.ID, model.NewReview{Rating: 5, Text: fakeText})
	if err != nil || rev.Status != model.StatusPending {
		t.Fatalf("%+v %v", rev, err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindReview, ObjectID: rev.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Resubmit replaces and goes pending again.
	rev2, err := e.svc.PutReview(ctx, r1, sp.ID, model.NewReview{Rating: 3, Text: fakeText + " updated"})
	if err != nil || rev2.Status != model.StatusPending {
		t.Fatalf("resubmit %+v %v", rev2, err)
	}
	if rev2.ID != rev.ID {
		t.Fatal("review id should stay stable on upsert")
	}
	stored, _ := e.store.Review(rev.ID)
	if stored.Rating != 3 || stored.Status != model.StatusPending {
		t.Fatalf("stored %+v", stored)
	}
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindReview, ObjectID: rev.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	revB, err := e.svc.PutReview(ctx, r2, sp.ID, model.NewReview{Rating: 4, Text: fakeText})
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
	if item.ReviewCount != 2 || item.AverageRating == nil || *item.AverageRating != 3.5 {
		t.Fatalf("ratings %+v count=%d", item.AverageRating, item.ReviewCount)
	}
	if len(item.LatestReviews) != 2 {
		t.Fatalf("latest %d", len(item.LatestReviews))
	}
	for _, lr := range item.LatestReviews {
		if strings.Contains(lr.AuthorName, " ") {
			t.Fatalf("author name not first-only: %q", lr.AuthorName)
		}
	}
}

func TestModerationDedup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	author := member(e, true, "Aigul")
	sp, err := e.svc.SuggestSpecialist(ctx, author, uuid.Must(uuid.NewV7()), model.NewSpecialist{
		Name: fakeName, Profession: "lawyer", DistrictID: uuid.New(), Needs: []string{"adhd"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eid := uuid.New()
	if err := e.svc.ApplyModerationDecided(ctx, eid, model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindSpecialist, ObjectID: sp.ID, Approve: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ApplyModerationDecided(ctx, eid, model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: model.ModerationKindSpecialist, ObjectID: sp.ID, Approve: false, Reason: "spam",
	}); err != nil {
		t.Fatal(err)
	}
	s, _ := e.store.Specialist(sp.ID)
	if s.Status != model.StatusActive {
		t.Fatal("dedup failed")
	}
}
