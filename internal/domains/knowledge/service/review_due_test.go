package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/service"
	"birge/internal/platform/auth"
)

func staffMFA(role string) auth.Principal {
	p := staff(role)
	p.MFA = true
	return p
}

func publishForReviewDue(t *testing.T, svc *service.Service, kind, lang string, source *string) model.Version {
	t.Helper()
	topic := createTopic(t, svc, kind)
	authorRole := model.ReviewerRoleFor(kind)
	author := staff(authorRole)
	v := draftAndSubmit(t, svc, author, topic, lang, source)
	reviewer := staff(authorRole)
	pub, err := svc.Publish(context.Background(), reviewer, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestReviewDueRolesAndOrdering(t *testing.T) {
	svc, store, clk := newEnv(t)
	src := "https://example.invalid/law"
	rights := publishForReviewDue(t, svc, model.KindRights, model.LangRU, &src)
	selfhelp := publishForReviewDue(t, svc, model.KindSelfhelp, model.LangRU, nil)

	// Advance past both review-due times; mark via timer handler.
	clk.Advance(svc.Settings.RightsTTL - model.ReviewDueLead + time.Hour)
	for _, id := range []uuid.UUID{rights.ID, selfhelp.ID} {
		tx, commit := store.Tx()
		if err := svc.HandleReviewDue(context.Background(), tx, id); err != nil {
			t.Fatal(err)
		}
		commit()
	}

	// Lawyer sees only rights.
	page, err := svc.StaffListReviewDue(context.Background(), staffMFA(model.RoleLawyer), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != rights.ID || page.Items[0].Kind != model.KindRights {
		t.Fatalf("lawyer page = %+v", page.Items)
	}

	// Psychologist sees only self-help.
	page, err = svc.StaffListReviewDue(context.Background(), staffMFA(model.RolePsychologist), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != selfhelp.ID {
		t.Fatalf("psychologist page = %+v", page.Items)
	}

	// Admin sees both, oldest expiry first (rights TTL shorter → earlier expires_at).
	page, err = svc.StaffListReviewDue(context.Background(), staffMFA(model.RoleAdmin), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("admin page len = %d", len(page.Items))
	}
	if page.Items[0].ID != rights.ID || page.Items[1].ID != selfhelp.ID {
		t.Fatalf("order = %v, %v want rights then selfhelp", page.Items[0].Kind, page.Items[1].Kind)
	}

	// No MFA → forbidden.
	noMFA := staffMFA(model.RoleAdmin)
	noMFA.MFA = false
	if _, err := svc.StaffListReviewDue(context.Background(), noMFA, ""); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("no mfa err = %v", err)
	}

	// Moderator → forbidden.
	mod := auth.Principal{IdentityID: uuid.Must(uuid.NewV7()), Staff: true, MFA: true, Roles: []string{"moderator"}}
	if _, err := svc.StaffListReviewDue(context.Background(), mod, ""); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("moderator err = %v", err)
	}
}

func TestHandleReviewDueMarksVersion(t *testing.T) {
	svc, store, _ := newEnv(t)
	src := "https://example.invalid/law"
	pub := publishForReviewDue(t, svc, model.KindRights, model.LangEN, &src)
	tx, commit := store.Tx()
	if err := svc.HandleReviewDue(context.Background(), tx, pub.ID); err != nil {
		t.Fatal(err)
	}
	commit()
	v, ok := store.Version(pub.ID)
	if !ok || v.ReviewDueAt == nil {
		t.Fatalf("review_due_at not set: %+v", v)
	}
}

func TestSlugSafe(t *testing.T) {
	if !model.SlugSafe("apply") {
		t.Fatal("apply should be safe")
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/b", `a\b`, "A_UPPER", "-dash"} {
		if model.SlugSafe(bad) {
			t.Fatalf("%q should be refused", bad)
		}
	}
}
