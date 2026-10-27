package service_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/knowledgetest"
	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
	"birge/internal/domains/knowledge/service"
	"birge/internal/platform/auth"
)

// errStore fails every InTx call so service methods surface store errors.
type errStore struct{ err error }

func (e errStore) InTx(context.Context, func(port.Tx) error) error { return e.err }

func letterSuffix(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return string(letters[i%26]) + string(letters[(i*3)%26])
}

func TestCreateTopicRoleMatrix(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	title := "DEMO topic"
	titles := map[string]*string{"ru": &title}

	cases := []struct {
		name    string
		role    string
		kind    string
		wantErr error
	}{
		{"admin rights", model.RoleAdmin, model.KindRights, nil},
		{"admin selfhelp", model.RoleAdmin, model.KindSelfhelp, nil},
		{"lawyer rights", model.RoleLawyer, model.KindRights, nil},
		{"lawyer selfhelp", model.RoleLawyer, model.KindSelfhelp, model.ErrForbidden},
		{"psychologist selfhelp", model.RolePsychologist, model.KindSelfhelp, nil},
		{"psychologist rights", model.RolePsychologist, model.KindRights, model.ErrForbidden},
		{"moderator", "moderator", model.KindRights, model.ErrForbidden},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code := "role" + letterSuffix(i) + "topic"
			_, err := svc.CreateTopic(ctx, staff(tc.role), tc.kind, code, titles)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateTopicUnauthorizedAndInvalid(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	title := "DEMO"
	titles := map[string]*string{"ru": &title}

	if _, err := svc.CreateTopic(ctx, auth.Principal{}, model.KindRights, "demo_code", titles); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("anonymous = %v", err)
	}
	if _, err := svc.CreateTopic(ctx, member(), model.KindRights, "demo_code", titles); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("member = %v", err)
	}
	if _, err := svc.CreateTopic(ctx, staff(model.RoleAdmin), "faq", "demo_code", titles); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad kind = %v", err)
	}
	if _, err := svc.CreateTopic(ctx, staff(model.RoleAdmin), model.KindRights, "Bad-Code", titles); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad code = %v", err)
	}
	if _, err := svc.CreateTopic(ctx, staff(model.RoleAdmin), model.KindRights, "demo_empty", map[string]*string{}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("no titles = %v", err)
	}
}

func TestWrongRoleDraftAndReject(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()

	rights := createTopic(t, svc, model.KindRights)
	selfhelp := createTopic(t, svc, model.KindSelfhelp)
	src := "https://example.invalid/law"

	if _, err := svc.CreateDraft(ctx, staff(model.RoleLawyer), uuid.Must(uuid.NewV7()), selfhelp.ID, nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO body",
	}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("lawyer drafting selfhelp = %v", err)
	}
	if _, err := svc.CreateDraft(ctx, staff(model.RolePsychologist), uuid.Must(uuid.NewV7()), rights.ID, nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO body", SourceURL: &src,
	}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("psychologist drafting rights = %v", err)
	}
	// Admin creates topics but does not author drafts (author role is reviewer-for-kind).
	if _, err := svc.CreateDraft(ctx, staff(model.RoleAdmin), uuid.Must(uuid.NewV7()), rights.ID, nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO body", SourceURL: &src,
	}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("admin drafting rights = %v", err)
	}

	v := draftAndSubmit(t, svc, staff(model.RoleLawyer), rights, model.LangRU, &src)
	if _, err := svc.Reject(ctx, staff(model.RolePsychologist), v.ID, model.RejectUnclear); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("psychologist rejecting rights = %v", err)
	}
	if _, err := svc.Reject(ctx, staff(model.RoleAdmin), v.ID, model.RejectUnclear); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("admin rejecting = %v", err)
	}
}

func TestAuthorCannotReject(t *testing.T) {
	svc, _, _ := newEnv(t)
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, nil)
	if _, err := svc.Reject(context.Background(), author, v.ID, model.RejectOther); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("author reject = %v", err)
	}
}

func TestEditNonDraftConflicts(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, nil)

	if _, err := svc.UpdateDraft(ctx, author, v.ID, model.Content{Title: "DEMO2", Body: "DEMO body 2"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("update in_review = %v", err)
	}
	if _, err := svc.SubmitForReview(ctx, author, v.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("submit twice = %v", err)
	}

	reviewer := staff(model.RolePsychologist)
	pub, err := svc.Publish(ctx, reviewer, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateDraft(ctx, author, pub.ID, model.Content{Title: "DEMO3", Body: "DEMO body 3"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("update published = %v", err)
	}
}

func TestPublishTwiceConflicts(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	reviewer := staff(model.RolePsychologist)
	v := draftAndSubmit(t, svc, author, topic, model.LangRU, nil)
	if _, err := svc.Publish(ctx, reviewer, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Publish(ctx, reviewer, v.ID); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("publish twice = %v", err)
	}
	if _, err := svc.Reject(ctx, reviewer, v.ID, model.RejectUnclear); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("reject published = %v", err)
	}
}

func TestUnknownIDs(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	m := member()
	unknown := uuid.Must(uuid.NewV7())
	author := staff(model.RolePsychologist)

	if _, err := svc.ListTopicArticles(ctx, m, unknown, model.LangRU); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("list articles unknown topic = %v", err)
	}
	if _, err := svc.GetArticle(ctx, m, unknown, model.LangRU); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("get article = %v", err)
	}
	if _, err := svc.UpdateDraft(ctx, author, unknown, model.Content{Title: "DEMO", Body: "DEMO body"}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("update unknown = %v", err)
	}
	if _, err := svc.SubmitForReview(ctx, author, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("submit unknown = %v", err)
	}
	if _, err := svc.Publish(ctx, author, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("publish unknown = %v", err)
	}
	if _, err := svc.Reject(ctx, author, unknown, model.RejectOther); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("reject unknown = %v", err)
	}
	if _, err := svc.CreateDraft(ctx, author, uuid.Must(uuid.NewV7()), unknown, nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO body",
	}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("draft unknown topic = %v", err)
	}
}

func TestNilIDsInvalid(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	author := staff(model.RolePsychologist)
	m := member()

	if _, err := svc.UpdateDraft(ctx, author, uuid.Nil, model.Content{Title: "DEMO", Body: "DEMO"}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil version update = %v", err)
	}
	if _, err := svc.SubmitForReview(ctx, author, uuid.Nil); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil submit = %v", err)
	}
	if _, err := svc.Publish(ctx, author, uuid.Nil); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil publish = %v", err)
	}
	if _, err := svc.Reject(ctx, author, uuid.Nil, model.RejectOther); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil reject = %v", err)
	}
	if _, err := svc.Reject(ctx, author, uuid.Must(uuid.NewV7()), "nope"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad reason = %v", err)
	}
	if _, err := svc.CreateDraft(ctx, author, uuid.Nil, uuid.Must(uuid.NewV7()), nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO",
	}); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil idempotency = %v", err)
	}
	if _, err := svc.GetArticle(ctx, m, uuid.Nil, model.LangRU); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("nil article = %v", err)
	}
	if _, err := svc.ListTopics(ctx, m, "bad", model.LangRU); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad kind list = %v", err)
	}
	if _, err := svc.PublishedFragments(ctx, "xx", model.KindRights); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad frag lang = %v", err)
	}
}

func TestUpdateDraftWrongAuthor(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	topic := createTopic(t, svc, model.KindSelfhelp)
	author := staff(model.RolePsychologist)
	other := staff(model.RolePsychologist)
	key := uuid.Must(uuid.NewV7())
	v, err := svc.CreateDraft(ctx, author, key, topic.ID, nil, model.LangRU, model.Content{
		Title: "DEMO", Body: "DEMO body",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateDraft(ctx, other, v.ID, model.Content{Title: "DEMO2", Body: "DEMO body 2"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("other author update = %v", err)
	}
	if _, err := svc.SubmitForReview(ctx, other, v.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("other author submit = %v", err)
	}
}

func TestMemberAuthRequired(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	topic := createTopic(t, svc, model.KindSelfhelp)

	if _, err := svc.ListTopics(ctx, auth.Principal{}, model.KindSelfhelp, model.LangRU); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("anon list = %v", err)
	}
	if _, err := svc.ListTopics(ctx, staff(model.RoleAdmin), model.KindSelfhelp, model.LangRU); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("staff list = %v", err)
	}
	if _, err := svc.ListTopicArticles(ctx, auth.Principal{}, topic.ID, model.LangRU); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("anon articles = %v", err)
	}
	if _, err := svc.GetArticle(ctx, auth.Principal{}, uuid.Must(uuid.NewV7()), model.LangRU); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("anon get = %v", err)
	}
}

func TestStaffListUnauthorized(t *testing.T) {
	svc, _, _ := newEnv(t)
	ctx := context.Background()
	if _, err := svc.StaffListTopics(ctx, auth.Principal{}); !errors.Is(err, model.ErrUnauthorized) {
		t.Fatalf("anon staff topics = %v", err)
	}
	if _, err := svc.StaffListArticles(ctx, member(), "", ""); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("member staff articles = %v", err)
	}
	if _, err := svc.StaffListArticles(ctx, staff(model.RoleAdmin), "pending", ""); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad status = %v", err)
	}
	if _, err := svc.StaffListArticles(ctx, staff(model.RoleAdmin), "", "%%%"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("bad cursor = %v", err)
	}
}

func TestStoreErrorsPropagated(t *testing.T) {
	boom := errors.New("store down")
	svc := &service.Service{
		Store:  errStore{err: boom},
		Clock:  &knowledgetest.Clock{T: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx := context.Background()
	m := member()
	admin := staff(model.RoleAdmin)
	title := "DEMO"
	ops := []struct {
		name string
		fn   func() error
	}{
		{"ListTopics", func() error { _, err := svc.ListTopics(ctx, m, model.KindRights, model.LangRU); return err }},
		{"StaffListTopics", func() error { _, err := svc.StaffListTopics(ctx, admin); return err }},
		{"CreateTopic", func() error {
			_, err := svc.CreateTopic(ctx, admin, model.KindRights, "demo_store", map[string]*string{"ru": &title})
			return err
		}},
		{"GetArticle", func() error { _, err := svc.GetArticle(ctx, m, uuid.Must(uuid.NewV7()), model.LangRU); return err }},
		{"Publish", func() error { _, err := svc.Publish(ctx, staff(model.RoleLawyer), uuid.Must(uuid.NewV7())); return err }},
		{"PublishedFragments", func() error { _, err := svc.PublishedFragments(ctx, model.LangRU, model.KindRights); return err }},
	}
	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			if err := op.fn(); !errors.Is(err, boom) {
				t.Fatalf("err = %v, want store error", err)
			}
		})
	}
}
