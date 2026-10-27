package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
)

func TestKindIsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"rights", model.KindRights, true},
		{"selfhelp", model.KindSelfhelp, true},
		{"empty", "", false},
		{"unknown", "faq", false},
		{"mixed case", "Rights", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := model.KindIsValid(tc.in); got != tc.want {
				t.Fatalf("KindIsValid(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLangIsValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{model.LangKK, true},
		{model.LangRU, true},
		{model.LangEN, true},
		{"", false},
		{"fr", false},
		{"RU", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := model.LangIsValid(tc.in); got != tc.want {
				t.Fatalf("LangIsValid(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestStatusIsValid(t *testing.T) {
	t.Parallel()
	// Lifecycle: draft → in_review → published → retired (and reject: in_review → draft).
	cases := []struct {
		in   string
		want bool
	}{
		{model.StatusDraft, true},
		{model.StatusInReview, true},
		{model.StatusPublished, true},
		{model.StatusRetired, true},
		{"", false},
		{"pending", false},
		{"rejected", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := model.StatusIsValid(tc.in); got != tc.want {
				t.Fatalf("StatusIsValid(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRejectIsValid(t *testing.T) {
	t.Parallel()
	valid := []string{
		model.RejectInaccurate, model.RejectOutdatedLaw, model.RejectUnclear,
		model.RejectUnsafeAdvice, model.RejectMissingSource, model.RejectOther,
	}
	for _, r := range valid {
		t.Run(r, func(t *testing.T) {
			t.Parallel()
			if !model.RejectIsValid(r) {
				t.Fatalf("RejectIsValid(%q) = false", r)
			}
		})
	}
	for _, r := range []string{"", "typo", "INACCURATE", "missing source"} {
		t.Run("bad_"+r, func(t *testing.T) {
			t.Parallel()
			if model.RejectIsValid(r) {
				t.Fatalf("RejectIsValid(%q) = true", r)
			}
		})
	}
}

func TestReviewerRoleFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind, want string
	}{
		{model.KindRights, model.RoleLawyer},
		{model.KindSelfhelp, model.RolePsychologist},
		{"", ""},
		{"other", ""},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			if got := model.ReviewerRoleFor(tc.kind); got != tc.want {
				t.Fatalf("ReviewerRoleFor(%q) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}

func TestTopicTitleIn(t *testing.T) {
	t.Parallel()
	kk, ru, en := "Қаз", "Рус", "Eng"
	topic := model.Topic{Code: "demo_code", TitleKK: &kk, TitleRU: &ru, TitleEN: &en}
	cases := []struct {
		lang, want string
	}{
		{model.LangKK, "Қаз"},
		{model.LangRU, "Рус"},
		{model.LangEN, "Eng"},
		{"fr", "demo_code"},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			t.Parallel()
			if got := topic.TitleIn(tc.lang); got != tc.want {
				t.Fatalf("TitleIn(%q) = %q, want %q", tc.lang, got, tc.want)
			}
		})
	}

	blank := "   "
	emptyTitles := model.Topic{Code: "fallback", TitleRU: &blank}
	if got := emptyTitles.TitleIn(model.LangRU); got != "fallback" {
		t.Fatalf("blank title = %q, want code", got)
	}
	if got := emptyTitles.TitleIn(model.LangKK); got != "fallback" {
		t.Fatalf("nil title = %q, want code", got)
	}
}

func TestAvailabilityOf(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		kind      string
		expiresAt time.Time
		want      string
	}{
		{"rights current", model.KindRights, now.Add(time.Hour), model.AvailabilityCurrent},
		{"rights exactly expired", model.KindRights, now, model.AvailabilityBeingRechecked},
		{"rights past", model.KindRights, now.Add(-time.Hour), model.AvailabilityBeingRechecked},
		{"selfhelp past still current", model.KindSelfhelp, now.Add(-time.Hour), model.AvailabilityCurrent},
		{"selfhelp future", model.KindSelfhelp, now.Add(time.Hour), model.AvailabilityCurrent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := model.AvailabilityOf(tc.kind, tc.expiresAt, now); got != tc.want {
				t.Fatalf("AvailabilityOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{"ok short", "ab", false},
		{"ok underscore", "demo_rights", false},
		{"ok max", strings.Repeat("a", 40), false},
		{"too short", "a", true},
		{"too long", strings.Repeat("a", 41), true},
		{"digits", "demo1", true},
		{"dash", "demo-x", true},
		{"upper", "Demo", true},
		{"empty", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := model.ValidateCode(tc.code)
			if tc.wantErr && !errors.Is(err, model.ErrInvalidInput) {
				t.Fatalf("err = %v, want invalid", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestValidateSlugAndSlugSafe(t *testing.T) {
	t.Parallel()
	ok := []string{"ab", "apply", "a_1", strings.Repeat("a", 60)}
	for _, s := range ok {
		t.Run("ok_"+s, func(t *testing.T) {
			t.Parallel()
			if err := model.ValidateSlug(s); err != nil {
				t.Fatalf("ValidateSlug(%q) = %v", s, err)
			}
			if !model.SlugSafe(s) {
				t.Fatalf("SlugSafe(%q) = false", s)
			}
		})
	}
	bad := []string{"", "a", strings.Repeat("a", 61), "A_upper", "-dash", "a/b", `a\b`, ".", "..", "../x"}
	for _, s := range bad {
		t.Run("bad_"+s, func(t *testing.T) {
			t.Parallel()
			if model.SlugSafe(s) {
				t.Fatalf("SlugSafe(%q) = true", s)
			}
		})
	}
	if err := model.ValidateSlug("a/b"); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("ValidateSlug slash = %v", err)
	}
}

func TestArticleSlugFromID(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	got := model.ArticleSlugFromID(id)
	want := "a_01900000000070008000000000000001"
	if got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
	if !model.SlugSafe(got) {
		t.Fatalf("generated slug not safe: %q", got)
	}
}

func TestValidateContent(t *testing.T) {
	t.Parallel()
	src := "https://example.invalid/law"
	cases := []struct {
		name    string
		c       model.Content
		wantErr bool
	}{
		{"ok", model.Content{Title: "DEMO", Body: "DEMO body", SourceURL: &src}, false},
		{"ok no source", model.Content{Title: "DEMO", Body: "DEMO body"}, false},
		{"empty title", model.Content{Title: "  ", Body: "DEMO body"}, true},
		{"empty body", model.Content{Title: "DEMO", Body: " \t"}, true},
		{"title too long", model.Content{Title: strings.Repeat("т", model.MaxTitleRunes+1), Body: "DEMO"}, true},
		{"body too long", model.Content{Title: "DEMO", Body: strings.Repeat("б", model.MaxBodyRunes+1)}, true},
		{"title max ok", model.Content{Title: strings.Repeat("t", model.MaxTitleRunes), Body: "DEMO"}, false},
		{"invalid utf8 title", model.Content{Title: string([]byte{0xff}), Body: "DEMO"}, true},
		{"invalid utf8 body", model.Content{Title: "DEMO", Body: string([]byte{0xff, 0xfe})}, true},
		{"empty source", model.Content{Title: "DEMO", Body: "DEMO", SourceURL: ptr("  ")}, true},
		{"source too long", model.Content{Title: "DEMO", Body: "DEMO", SourceURL: ptr("https://example.invalid/" + strings.Repeat("x", model.MaxSourceURL))}, true},
		{"source not url", model.Content{Title: "DEMO", Body: "DEMO", SourceURL: ptr("not-a-url")}, true},
		{"source ftp", model.Content{Title: "DEMO", Body: "DEMO", SourceURL: ptr("ftp://example.invalid/x")}, true},
		{"source no host", model.Content{Title: "DEMO", Body: "DEMO", SourceURL: ptr("https:///path")}, true},
		{"source http ok", model.Content{Title: "DEMO", Body: "DEMO", SourceURL: ptr("http://example.invalid/x")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := model.ValidateContent(tc.c)
			if tc.wantErr && !errors.Is(err, model.ErrInvalidInput) {
				t.Fatalf("err = %v, want invalid", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}

func TestValidateTopicTitle(t *testing.T) {
	t.Parallel()
	if err := model.ValidateTopicTitle(nil); err != nil {
		t.Fatalf("nil = %v", err)
	}
	ok := "DEMO topic"
	if err := model.ValidateTopicTitle(&ok); err != nil {
		t.Fatalf("ok = %v", err)
	}
	blank := "  "
	if err := model.ValidateTopicTitle(&blank); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("blank = %v", err)
	}
	long := strings.Repeat("t", model.MaxTopicTitle+1)
	if err := model.ValidateTopicTitle(&long); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("long = %v", err)
	}
	bad := string([]byte{0xff})
	if err := model.ValidateTopicTitle(&bad); !errors.Is(err, model.ErrInvalidInput) {
		t.Fatalf("utf8 = %v", err)
	}
	max := strings.Repeat("t", model.MaxTopicTitle)
	if err := model.ValidateTopicTitle(&max); err != nil {
		t.Fatalf("max = %v", err)
	}
}

func ptr(s string) *string { return &s }
