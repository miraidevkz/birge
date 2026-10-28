package service_test

import (
	"bytes"
	"context"
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

func (e *env) clock() *directorytest.Clock { return e.svc.Clock.(*directorytest.Clock) }

func (e *env) decide(t *testing.T, kind string, id uuid.UUID, approve bool, reason string) {
	t.Helper()
	if err := e.svc.ApplyModerationDecided(context.Background(), uuid.New(), model.ModerationDecidedData{
		ItemID: uuid.New(), Kind: kind, ObjectID: id, Approve: approve, Reason: reason,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) suggest(t *testing.T, p auth.Principal, in model.NewSpecialist) model.SpecialistView {
	t.Helper()
	v, err := e.svc.SuggestSpecialist(context.Background(), p, uuid.Must(uuid.NewV7()), in)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func spec(profession string, district uuid.UUID, needs ...string) model.NewSpecialist {
	return model.NewSpecialist{Name: fakeName, Profession: profession, DistrictID: district, Needs: needs}
}

func strp(s string) *string { return &s }

func TestDenyByDefault(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	verified := member(e, true, "Aigul")
	sp := e.suggest(t, verified, spec("psychologist", uuid.New()))
	unknown := auth.Principal{IdentityID: uuid.New(), MemberID: uuid.New()} // no profile: deleted
	cases := map[string]struct {
		p    auth.Principal
		want error
	}{
		"anonymous":  {auth.Principal{}, model.ErrUnauthorized},
		"staff":      {auth.Principal{IdentityID: uuid.New(), Staff: true, Roles: []string{"admin"}, MFA: true}, model.ErrUnauthorized},
		"unverified": {member(e, false, "Dana"), model.ErrForbidden},
		"no profile": {unknown, model.ErrForbidden},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := e.svc.SuggestSpecialist(ctx, c.p, uuid.Must(uuid.NewV7()), spec("lawyer", uuid.New())); !errors.Is(err, c.want) {
				t.Fatalf("suggest: %v", err)
			}
			if _, err := e.svc.PutReview(ctx, c.p, sp.ID, model.NewReview{Rating: 5, Text: fakeText}); !errors.Is(err, c.want) {
				t.Fatalf("review: %v", err)
			}
		})
	}
	// Search needs a member session but not verification.
	for _, p := range []auth.Principal{{}, cases["staff"].p} {
		if _, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, ""); !errors.Is(err, model.ErrUnauthorized) {
			t.Fatalf("search %+v: %v", p, err)
		}
	}
	if _, err := e.svc.SearchSpecialists(ctx, cases["unverified"].p, nil, nil, nil, ""); err != nil {
		t.Fatalf("unverified search: %v", err)
	}
	if n := len(e.store.EventsOfType(model.EventItemSubmitted)); n != 1 {
		t.Fatalf("refused calls submitted items: %d events", n)
	}

	// Membership down: fail closed, never treat her as verified.
	e.members.Fail = errors.New("membership down")
	if _, err := e.svc.SuggestSpecialist(ctx, verified, uuid.Must(uuid.NewV7()), spec("lawyer", uuid.New())); err == nil {
		t.Fatal("suggest while membership is down")
	}
	e.svc.Members = nil
	if _, err := e.svc.PutReview(ctx, verified, sp.ID, model.NewReview{Rating: 5, Text: fakeText}); err == nil {
		t.Fatal("review without a member port")
	}
}

func TestSuggestValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := member(e, true, "Aigul")
	d := uuid.New()
	tooMany := make([]string, model.MaxNeeds+1)
	for i := range tooMany {
		tooMany[i] = "autism"
	}
	cases := []struct {
		name string
		key  uuid.UUID
		in   model.NewSpecialist
	}{
		{"nil idempotency key", uuid.Nil, spec("lawyer", d)},
		{"name too short", uuid.New(), model.NewSpecialist{Name: " a ", Profession: "lawyer", DistrictID: d}},
		{"name too long", uuid.New(), model.NewSpecialist{Name: strings.Repeat("я", model.MaxNameLen+1), Profession: "lawyer", DistrictID: d}},
		{"profession not a code", uuid.New(), spec("Lawyer!", d)},
		{"no district", uuid.New(), spec("lawyer", uuid.Nil)},
		{"too many needs", uuid.New(), spec("lawyer", d, tooMany...)},
		{"need not a code", uuid.New(), spec("lawyer", d, "Autism")},
		{"contacts too long", uuid.New(), model.NewSpecialist{Name: fakeName, Profession: "lawyer", DistrictID: d, Contacts: strp(strings.Repeat("1", model.MaxContactsLen+1))}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := e.svc.SuggestSpecialist(ctx, p, c.key, c.in); !errors.Is(err, model.ErrInvalidInput) {
				t.Fatalf("want invalid input, got %v", err)
			}
		})
	}

	// Contacts are trimmed; blank contacts are none; the name is trimmed.
	v, err := e.svc.SuggestSpecialist(ctx, p, uuid.Must(uuid.NewV7()), model.NewSpecialist{
		Name: "  " + fakeName + " ", Profession: "lawyer", DistrictID: d, Contacts: strp("  +7 700 000 0000  "),
	})
	if err != nil || v.Name != fakeName || v.Contacts == nil || *v.Contacts != "+7 700 000 0000" || v.Status != model.StatusPending {
		t.Fatalf("%+v %v", v, err)
	}
	if v.LatestReviews == nil || v.ReviewCount != 0 {
		t.Fatal("new card must render an empty review list")
	}
	v, err = e.svc.SuggestSpecialist(ctx, p, uuid.Must(uuid.NewV7()), model.NewSpecialist{Name: fakeName, Profession: "lawyer", DistrictID: d, Contacts: strp("   ")})
	if err != nil || v.Contacts != nil {
		t.Fatalf("blank contacts: %+v %v", v.Contacts, err)
	}
	ev := e.store.EventsOfType(model.EventItemSubmitted)
	if d := ev[len(ev)-1].Data.(model.ItemSubmittedData); d.ObjectType != model.ObjectSpecialist || d.ObjectID != v.ID {
		t.Fatalf("event %+v", d)
	}

	// The same key from another member is another card.
	key := uuid.Must(uuid.NewV7())
	a, _ := e.svc.SuggestSpecialist(ctx, p, key, spec("lawyer", d))
	b, err := e.svc.SuggestSpecialist(ctx, member(e, true, "Dana"), key, spec("lawyer", d))
	if err != nil || a.ID == b.ID {
		t.Fatalf("idempotency key shared across members: %v", err)
	}
}

func TestSuggestFailuresLeaveNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := member(e, true, "Aigul")
	key := uuid.Must(uuid.NewV7())
	for _, method := range []string{"InsertSpecialist", "Publish:" + model.EventItemSubmitted} {
		e.store.FailOn = func(m string) error {
			if m == method {
				return errors.New("database down")
			}
			return nil
		}
		if _, err := e.svc.SuggestSpecialist(ctx, p, key, spec("lawyer", uuid.New())); err == nil {
			t.Fatalf("%s failure not returned", method)
		}
	}
	e.store.FailOn = nil
	if len(e.store.EventsOfType(model.EventItemSubmitted)) != 0 {
		t.Fatal("event without its card")
	}
	// The retry with the same key creates the card once.
	v, err := e.svc.SuggestSpecialist(ctx, p, key, spec("lawyer", uuid.New()))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := e.store.Specialist(v.ID); !ok {
		t.Fatal("retry did not store the card")
	}
	// A replay whose lookup fails is an error, not a second card.
	e.store.FailOn = func(m string) error {
		if m == "GetSpecialistByIdempotencyKey" {
			return errors.New("database down")
		}
		return nil
	}
	if _, err := e.svc.SuggestSpecialist(ctx, p, key, spec("lawyer", uuid.New())); err == nil {
		t.Fatal("replay lookup failure not returned")
	}
}

func TestSearchShowsApprovedOnly(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := member(e, true, "Aigul")
	almaty, astana := uuid.New(), uuid.New()
	active := e.suggest(t, p, spec("speech_therapist", almaty, "autism", "cerebral_palsy"))
	e.decide(t, model.ModerationKindSpecialist, active.ID, true, "")
	other := e.suggest(t, p, spec("psychologist", astana, "adhd"))
	e.decide(t, model.ModerationKindSpecialist, other.ID, true, "")
	pending := e.suggest(t, p, spec("speech_therapist", almaty, "autism"))
	rejected := e.suggest(t, p, spec("speech_therapist", almaty, "autism"))
	e.decide(t, model.ModerationKindSpecialist, rejected.ID, false, "spam")

	ids := func(page model.SearchPage) map[uuid.UUID]bool {
		out := map[uuid.UUID]bool{}
		for _, it := range page.Items {
			out[it.ID] = true
			if it.Status != model.StatusActive {
				t.Fatalf("non-active card listed: %+v", it)
			}
		}
		return out
	}
	all, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	got := ids(all)
	if len(got) != 2 || !got[active.ID] || !got[other.ID] || got[pending.ID] || got[rejected.ID] {
		t.Fatalf("search %v", got)
	}
	filters := []struct {
		name             string
		profession, need *string
		district         *uuid.UUID
		want             uuid.UUID
	}{
		{"profession", strp("speech_therapist"), nil, nil, active.ID},
		{"need", nil, strp("adhd"), nil, other.ID},
		{"district", nil, nil, &almaty, active.ID},
		{"all three", strp("psychologist"), strp("adhd"), &astana, other.ID},
	}
	for _, f := range filters {
		page, err := e.svc.SearchSpecialists(ctx, p, f.profession, f.need, f.district, "")
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != f.want {
			t.Fatalf("%s: %+v %v", f.name, page.Items, err)
		}
	}
	if page, _ := e.svc.SearchSpecialists(ctx, p, strp("lawyer"), nil, nil, ""); len(page.Items) != 0 || page.Items == nil {
		t.Fatalf("no match must be an empty list: %+v", page.Items)
	}
	for _, bad := range []struct {
		profession, need *string
		cursor           string
	}{{strp("Bad Code"), nil, ""}, {nil, strp("x"), ""}, {nil, nil, "!!"}, {nil, nil, "bm9kb3Q"}, {nil, nil, "YWJjLnh5eg"}, {nil, nil, "MTIzLm5vdC1hLXV1aWQ"}} {
		if _, err := e.svc.SearchSpecialists(ctx, p, bad.profession, bad.need, nil, bad.cursor); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}

func TestSearchPagingAndReviews(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := member(e, true, "Aigul")
	start := e.clock().T
	var ids []uuid.UUID
	for range model.SearchPageLimit + 3 {
		v := e.suggest(t, p, spec("psychologist", uuid.New()))
		e.decide(t, model.ModerationKindSpecialist, v.ID, true, "")
		ids = append(ids, v.ID)
		e.clock().T = e.clock().T.Add(time.Minute)
	}
	e.clock().T = start.Add(time.Hour)
	first, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, "")
	if err != nil || len(first.Items) != model.SearchPageLimit || first.NextCursor == "" {
		t.Fatalf("first page %d %q %v", len(first.Items), first.NextCursor, err)
	}
	if first.Items[0].ID != ids[len(ids)-1] {
		t.Fatal("not newest first")
	}
	second, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, first.NextCursor)
	if err != nil || len(second.Items) != 3 || second.NextCursor != "" || second.Items[2].ID != ids[0] {
		t.Fatalf("second page %d %q %v", len(second.Items), second.NextCursor, err)
	}

	// Ratings and the latest three count active reviews only.
	target := ids[0]
	var reviews []uuid.UUID
	for _, rating := range []int{5, 4, 3, 2} {
		author := member(e, true, "Reviewer Fakelast")
		e.clock().T = e.clock().T.Add(time.Minute)
		r, err := e.svc.PutReview(ctx, author, target, model.NewReview{Rating: rating, Text: fakeText})
		if err != nil {
			t.Fatal(err)
		}
		if r.AuthorName != "Reviewer" {
			t.Fatalf("author name %q: first name only", r.AuthorName)
		}
		e.decide(t, model.ModerationKindReview, r.ID, true, "")
		reviews = append(reviews, r.ID)
	}
	pendingAuthor := member(e, true, "Pending")
	if _, err := e.svc.PutReview(ctx, pendingAuthor, target, model.NewReview{Rating: 1, Text: fakeText}); err != nil {
		t.Fatal(err)
	}
	rejectedAuthor := member(e, true, "Rejected")
	rr, err := e.svc.PutReview(ctx, rejectedAuthor, target, model.NewReview{Rating: 1, Text: fakeText})
	if err != nil {
		t.Fatal(err)
	}
	e.decide(t, model.ModerationKindReview, rr.ID, false, "")
	if got, _ := e.store.Review(rr.ID); got.RejectReason == nil || *got.RejectReason != "other" {
		t.Fatalf("rejection without a reason must record other: %+v", got.RejectReason)
	}

	page, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var card model.SpecialistView
	for _, it := range page.Items {
		if it.ID == target {
			card = it
		}
	}
	if card.ID != target {
		t.Fatal("reviewed card not found")
	}
	if card.ReviewCount != 4 || card.AverageRating == nil || *card.AverageRating != 3.5 {
		t.Fatalf("count %d avg %v", card.ReviewCount, card.AverageRating)
	}
	if len(card.LatestReviews) != model.LatestReviewsLimit || card.LatestReviews[0].ID != reviews[3] {
		t.Fatalf("latest reviews %+v", card.LatestReviews)
	}
	for _, r := range card.LatestReviews {
		if r.Status != model.StatusActive || r.AuthorName != "Reviewer" {
			t.Fatalf("latest review %+v", r)
		}
	}
	// A card without reviews has no average (not zero).
	if second.Items[0].AverageRating != nil || second.Items[0].Needs == nil {
		t.Fatalf("empty card %+v", second.Items[0])
	}

	for _, method := range []string{"SearchActiveSpecialists", "AverageActiveRating", "ListLatestActiveReviews"} {
		e.store.FailOn = func(m string) error {
			if m == method {
				return errors.New("database down")
			}
			return nil
		}
		if _, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, ""); err == nil {
			t.Fatalf("%s failure not returned", method)
		}
	}
	e.store.FailOn = nil
	e.members.Fail = errors.New("membership down")
	if _, err := e.svc.SearchSpecialists(ctx, p, nil, nil, nil, first.NextCursor); err == nil {
		t.Fatal("reviews rendered without author names")
	}
}

func TestPutReviewRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	adder, author := member(e, true, "Aigul"), member(e, true, "Dana")
	active := e.suggest(t, adder, spec("psychologist", uuid.New()))
	e.decide(t, model.ModerationKindSpecialist, active.ID, true, "")
	pending := e.suggest(t, adder, spec("psychologist", uuid.New()))
	rejected := e.suggest(t, adder, spec("psychologist", uuid.New()))
	e.decide(t, model.ModerationKindSpecialist, rejected.ID, false, "duplicate")

	cases := []struct {
		name string
		sp   uuid.UUID
		in   model.NewReview
		want error
	}{
		{"rating zero", active.ID, model.NewReview{Rating: 0, Text: fakeText}, model.ErrInvalidInput},
		{"rating six", active.ID, model.NewReview{Rating: 6, Text: fakeText}, model.ErrInvalidInput},
		{"blank text", active.ID, model.NewReview{Rating: 3, Text: "  \n "}, model.ErrInvalidInput},
		{"text too long", active.ID, model.NewReview{Rating: 3, Text: strings.Repeat("я", model.MaxReviewTextLen+1)}, model.ErrInvalidInput},
		{"unknown specialist", uuid.New(), model.NewReview{Rating: 3, Text: fakeText}, model.ErrNotFound},
		{"rejected specialist", rejected.ID, model.NewReview{Rating: 3, Text: fakeText}, model.ErrNotFound},
		{"pending specialist", pending.ID, model.NewReview{Rating: 3, Text: fakeText}, nil},
		{"active specialist", active.ID, model.NewReview{Rating: 3, Text: "  " + fakeText + "  "}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := e.svc.PutReview(ctx, author, c.sp, c.in)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if c.want == nil && (r.Status != model.StatusPending || r.Text != fakeText || r.AuthorName != "Dana") {
				t.Fatalf("review %+v", r)
			}
		})
	}
	evs := e.store.EventsOfType(model.EventItemSubmitted)
	if d := evs[len(evs)-1].Data.(model.ItemSubmittedData); d.ObjectType != model.ObjectReview {
		t.Fatalf("event %+v", d)
	}

	for _, method := range []string{"GetSpecialist", "UpsertReview", "Publish:" + model.EventItemSubmitted} {
		e.store.FailOn = func(m string) error {
			if m == method {
				return errors.New("database down")
			}
			return nil
		}
		if _, err := e.svc.PutReview(ctx, member(e, true, "Sara"), active.ID, model.NewReview{Rating: 4, Text: fakeText}); err == nil {
			t.Fatalf("%s failure not returned", method)
		}
	}
	e.store.FailOn = nil
}

func TestApplyModerationDecidedRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	adder, author := member(e, true, "Aigul"), member(e, true, "Dana")
	sp := e.suggest(t, adder, spec("psychologist", uuid.New()))

	// Other kinds and invalid events.
	if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{Kind: "listing", ObjectID: sp.ID}); err != nil {
		t.Fatalf("other kind: %v", err)
	}
	for _, c := range []struct {
		event uuid.UUID
		data  model.ModerationDecidedData
	}{
		{uuid.Nil, model.ModerationDecidedData{Kind: model.ModerationKindSpecialist, ObjectID: sp.ID}},
		{uuid.New(), model.ModerationDecidedData{Kind: model.ModerationKindReview}},
	} {
		if err := e.svc.ApplyModerationDecided(ctx, c.event, c.data); !errors.Is(err, model.ErrInvalidInput) {
			t.Fatalf("%+v: %v", c, err)
		}
	}
	// Unknown objects are acknowledged.
	e.decide(t, model.ModerationKindSpecialist, uuid.New(), true, "")
	e.decide(t, model.ModerationKindReview, uuid.New(), false, "spam")

	// A long reason is cut to the column limit.
	e.decide(t, model.ModerationKindSpecialist, sp.ID, false, strings.Repeat("r", model.MaxRejectReasonLen+5))
	got, _ := e.store.Specialist(sp.ID)
	if got.Status != model.StatusRejected || got.RejectReason == nil || len(*got.RejectReason) != model.MaxRejectReasonLen {
		t.Fatalf("rejected %+v", got)
	}
	// A decided card is final: a later approval changes nothing.
	e.decide(t, model.ModerationKindSpecialist, sp.ID, true, "")
	if got, _ := e.store.Specialist(sp.ID); got.Status != model.StatusRejected {
		t.Fatal("rejected card re-approved")
	}

	active := e.suggest(t, adder, spec("psychologist", uuid.New()))
	e.decide(t, model.ModerationKindSpecialist, active.ID, true, "")
	r, err := e.svc.PutReview(ctx, author, active.ID, model.NewReview{Rating: 2, Text: fakeText})
	if err != nil {
		t.Fatal(err)
	}
	e.decide(t, model.ModerationKindReview, r.ID, false, "inaccurate")
	if got, _ := e.store.Review(r.ID); got.Status != model.StatusRejected || *got.RejectReason != "inaccurate" {
		t.Fatalf("review %+v", got)
	}
	e.decide(t, model.ModerationKindReview, r.ID, true, "")
	if got, _ := e.store.Review(r.ID); got.Status != model.StatusRejected {
		t.Fatal("decided review changed")
	}

	// Store failures are returned (the consumer redelivers).
	pending := e.suggest(t, adder, spec("psychologist", uuid.New()))
	rev, err := e.svc.PutReview(ctx, member(e, true, "Sara"), active.ID, model.NewReview{Rating: 5, Text: fakeText})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method string
		kind   string
		object uuid.UUID
	}{
		{"MarkProcessed", model.ModerationKindSpecialist, pending.ID},
		{"GetSpecialist", model.ModerationKindSpecialist, pending.ID},
		{"ApproveSpecialist", model.ModerationKindSpecialist, pending.ID},
		{"GetReview", model.ModerationKindReview, rev.ID},
		{"ApproveReview", model.ModerationKindReview, rev.ID},
	} {
		e.store.FailOn = func(m string) error {
			if m == c.method {
				return errors.New("database down")
			}
			return nil
		}
		if err := e.svc.ApplyModerationDecided(ctx, uuid.New(), model.ModerationDecidedData{Kind: c.kind, ObjectID: c.object, Approve: true}); err == nil {
			t.Fatalf("%s failure not returned", c.method)
		}
	}
	e.store.FailOn = nil
	if got, _ := e.store.Specialist(pending.ID); got.Status != model.StatusPending {
		t.Fatal("failed decision applied")
	}
}

func TestModerationSummariesCarryNoContacts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	p := member(e, true, "Aigul")
	d := uuid.New()
	sp, err := e.svc.SuggestSpecialist(ctx, p, uuid.Must(uuid.NewV7()), model.NewSpecialist{
		Name: fakeName, Profession: "speech_therapist", DistrictID: d, Needs: []string{"autism", "adhd"}, Contacts: strp("+7 700 000 0000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := e.svc.SpecialistSummary(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sum != fakeName+" · speech_therapist · "+d.String()+" · autism, adhd" || strings.Contains(sum, "700") {
		t.Fatalf("summary %q", sum)
	}
	r, err := e.svc.PutReview(ctx, member(e, true, "Dana"), sp.ID, model.NewReview{Rating: 4, Text: fakeText})
	if err != nil {
		t.Fatal(err)
	}
	if text, err := e.svc.ReviewSummary(ctx, r.ID); err != nil || text != fakeText {
		t.Fatalf("review summary %q %v", text, err)
	}
	for _, id := range []uuid.UUID{uuid.Nil, uuid.New()} {
		if _, err := e.svc.SpecialistSummary(ctx, id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("specialist %v: %v", id, err)
		}
		if _, err := e.svc.ReviewSummary(ctx, id); !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("review %v: %v", id, err)
		}
	}
	e.store.FailOn = func(string) error { return errors.New("database down") }
	if _, err := e.svc.SpecialistSummary(ctx, sp.ID); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("outage reported as not found: %v", err)
	}
	if _, err := e.svc.ReviewSummary(ctx, r.ID); err == nil || errors.Is(err, model.ErrNotFound) {
		t.Fatalf("outage reported as not found: %v", err)
	}
}

type fakeEraser struct {
	calls []uuid.UUID
	err   error
}

func (f *fakeEraser) EraseMember(_ context.Context, id uuid.UUID, _ time.Time) error {
	f.calls = append(f.calls, id)
	return f.err
}

func TestDeletionErasesByMemberID(t *testing.T) {
	var logs bytes.Buffer
	eraser := &fakeEraser{}
	d := &service.Deletion{Store: eraser, Clock: &directorytest.Clock{T: time.Now()}, Logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	ctx := context.Background()
	if err := d.DeleteMember(ctx, uuid.Nil); err == nil || len(eraser.calls) != 0 {
		t.Fatal("nil member accepted")
	}
	id := uuid.New()
	if err := d.DeleteMember(ctx, id); err != nil || len(eraser.calls) != 1 || eraser.calls[0] != id {
		t.Fatalf("%v %v", err, eraser.calls)
	}
	if !strings.Contains(logs.String(), `"event":"MemberDataDeleted"`) {
		t.Fatalf("logs %s", logs.String())
	}
	eraser.err = errors.New("database down")
	if err := d.DeleteMember(ctx, id); err == nil {
		t.Fatal("erase failure not returned (the consumer must retry)")
	}
}
