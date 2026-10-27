package model_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/circle/model"
)

func district(id uuid.UUID) *uuid.UUID { return &id }

func candidate(format string, prefs model.Preferences, p model.Profile, created time.Time) model.Candidate {
	return model.Candidate{
		Request: model.TeaRequest{
			ID: uuid.Must(uuid.NewV7()), MemberID: uuid.Must(uuid.NewV7()),
			Format: format, Preferences: prefs, Status: model.RequestOpen,
			CreatedAt: created, ExpiresAt: created.Add(time.Hour),
		},
		Profile: p,
	}
}

func TestScorePair(t *testing.T) {
	d1, d2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	a := candidate(model.FormatMeeting, model.Preferences{}, model.Profile{
		Verified: true, DistrictID: district(d1), Lang: "ru",
		Children: []model.Child{{BirthYear: 2018, Needs: []string{"autism", "speech"}}},
	}, base)
	b := candidate(model.FormatMeeting, model.Preferences{}, model.Profile{
		Verified: true, DistrictID: district(d1), Lang: "ru",
		Children: []model.Child{{BirthYear: 2019, Needs: []string{"autism"}}},
	}, base.Add(time.Minute))

	score, ok := model.ScorePair(a, b, model.DefaultScoreWeights())
	if !ok {
		t.Fatal("expected compatible")
	}
	// district 3 + age 2 + need 2 + language 1 = 8
	if score != 8 {
		t.Fatalf("score=%d want 8", score)
	}

	b.Request.Format = model.FormatCall
	if _, ok := model.ScorePair(a, b, model.DefaultScoreWeights()); ok {
		t.Fatal("different format must be incompatible")
	}
	b.Request.Format = model.FormatMeeting

	a.Request.Preferences.SameDistrict = true
	b.Profile.DistrictID = district(d2)
	if _, ok := model.ScorePair(a, b, model.DefaultScoreWeights()); ok {
		t.Fatal("sameDistrict preference must reject")
	}
}

func TestScorePairPreferences(t *testing.T) {
	d := uuid.Must(uuid.NewV7())
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	a := candidate(model.FormatCall, model.Preferences{SimilarAge: true, SharedNeeds: true}, model.Profile{
		Verified: true, DistrictID: district(d), Lang: "kk",
		Children: []model.Child{{BirthYear: 2015, Needs: []string{"cp"}}},
	}, base)
	b := candidate(model.FormatCall, model.Preferences{}, model.Profile{
		Verified: true, DistrictID: district(d), Lang: "ru",
		Children: []model.Child{{BirthYear: 2020, Needs: []string{"autism"}}},
	}, base)
	if _, ok := model.ScorePair(a, b, model.DefaultScoreWeights()); ok {
		t.Fatal("age and needs preferences must reject")
	}
	b.Profile.Children = []model.Child{{BirthYear: 2016, Needs: []string{"cp"}}}
	score, ok := model.ScorePair(a, b, model.DefaultScoreWeights())
	if !ok || score < 5 {
		t.Fatalf("score=%d ok=%v", score, ok)
	}
}

func TestPairOpenRequestsGreedyAndTieBreak(t *testing.T) {
	d := uuid.Must(uuid.NewV7())
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	mk := func(offset time.Duration) model.Candidate {
		return candidate(model.FormatMeeting, model.Preferences{}, model.Profile{
			Verified: true, DistrictID: district(d), Lang: "ru",
			Children: []model.Child{{BirthYear: 2018, Needs: []string{"autism"}}},
		}, base.Add(offset))
	}
	c0, c1, c2, c3 := mk(0), mk(time.Minute), mk(2*time.Minute), mk(3*time.Minute)
	pairs, err := model.PairOpenRequests([]model.Candidate{c0, c1, c2, c3}, model.DefaultScoreWeights(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 {
		t.Fatalf("pairs=%d want 2", len(pairs))
	}
	// Earliest two requests should form the first pair (tie-break by created_at).
	ids := map[uuid.UUID]struct{}{pairs[0].A.Request.ID: {}, pairs[0].B.Request.ID: {}}
	if _, ok := ids[c0.Request.ID]; !ok {
		t.Fatal("first pair should include oldest request")
	}
	if _, ok := ids[c1.Request.ID]; !ok {
		t.Fatal("first pair should include second-oldest request")
	}
}

func TestPairOpenRequestsBlocksAndRematch(t *testing.T) {
	d := uuid.Must(uuid.NewV7())
	base := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	a := candidate(model.FormatMeeting, model.Preferences{}, model.Profile{
		Verified: true, DistrictID: district(d), Lang: "ru",
		Children: []model.Child{{BirthYear: 2018}},
	}, base)
	b := candidate(model.FormatMeeting, model.Preferences{}, model.Profile{
		Verified: true, DistrictID: district(d), Lang: "ru",
		Children: []model.Child{{BirthYear: 2018}},
	}, base.Add(time.Minute))

	blocked := func(x, y uuid.UUID) (bool, error) {
		lo, hi := model.CanonicalPair(a.Request.MemberID, b.Request.MemberID)
		return x == lo && y == hi, nil
	}
	pairs, err := model.PairOpenRequests([]model.Candidate{a, b}, model.DefaultScoreWeights(), blocked, nil)
	if err != nil || len(pairs) != 0 {
		t.Fatalf("blocked pairs=%v err=%v", pairs, err)
	}

	recent := func(x, y uuid.UUID) (bool, error) { return true, nil }
	pairs, err = model.PairOpenRequests([]model.Candidate{a, b}, model.DefaultScoreWeights(), nil, recent)
	if err != nil || len(pairs) != 0 {
		t.Fatalf("recent pairs=%v err=%v", pairs, err)
	}
}

func TestSharedNeedsAndAges(t *testing.T) {
	a := model.Profile{Children: []model.Child{{BirthYear: 2018, Needs: []string{"autism", "speech"}}, {BirthYear: 2020, Needs: []string{"autism"}}}}
	b := model.Profile{Children: []model.Child{{BirthYear: 2019, Needs: []string{"speech", "cp"}}}}
	shared := model.SharedNeeds(a, b)
	if len(shared) != 1 || shared[0] != "speech" {
		t.Fatalf("shared=%v", shared)
	}
	if !model.AgesWithin(a, b, 2) {
		t.Fatal("expected ages within window")
	}
	ages := model.ChildAges(a, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if len(ages) != 2 || ages[0] != 8 || ages[1] != 6 {
		t.Fatalf("ages=%v", ages)
	}
}

func TestCanonicalPair(t *testing.T) {
	a := uuid.MustParse("00000000-0000-7000-8000-000000000002")
	b := uuid.MustParse("00000000-0000-7000-8000-000000000001")
	lo, hi := model.CanonicalPair(a, b)
	if lo != b || hi != a {
		t.Fatalf("lo=%s hi=%s", lo, hi)
	}
	lo2, hi2 := model.CanonicalPair(b, a)
	if lo2 != lo || hi2 != hi {
		t.Fatal("canonical pair must be symmetric")
	}
}
