package model_test

import (
	"errors"
	"testing"

	"birge/internal/domains/moderation/model"
)

func TestValidateDecisionMatrix(t *testing.T) {
	type tc struct {
		d    model.Decision
		want map[model.Kind]bool // kinds where d is valid; all others invalid
	}
	all := map[model.Kind]bool{}
	for _, k := range model.Kinds {
		all[k] = true
	}
	chat := map[model.Kind]bool{model.KindReport: true, model.KindChatMessage: true}
	none := map[model.Kind]bool{}
	cases := map[string]tc{
		"approve":                       {model.Decision{Approve: true}, all},
		"approve with reason":           {model.Decision{Approve: true, Reason: "spam"}, none},
		"reject with reason":            {model.Decision{Reason: "spam"}, all},
		"reject without reason":         {model.Decision{}, none},
		"reject unknown reason":         {model.Decision{Reason: "because"}, none},
		"reject block author":           {model.Decision{Reason: "unsafe", BlockAuthor: true}, chat},
		"reject legal hold":             {model.Decision{Reason: "unsafe", LegalHold: true}, chat},
		"reject block and hold":         {model.Decision{Reason: "unsafe", BlockAuthor: true, LegalHold: true}, chat},
		"approve block author":          {model.Decision{Approve: true, BlockAuthor: true}, none},
		"approve legal hold":            {model.Decision{Approve: true, LegalHold: true}, none},
		"reject no reason block author": {model.Decision{BlockAuthor: true}, none},
	}
	for name, c := range cases {
		for _, k := range model.Kinds {
			err := model.ValidateDecision(k, c.d)
			if c.want[k] && err != nil {
				t.Errorf("%s / %s: unexpected error %v", name, k, err)
			}
			if !c.want[k] && !errors.Is(err, model.ErrInvalidInput) {
				t.Errorf("%s / %s: want ErrInvalidInput, got %v", name, k, err)
			}
		}
	}
	if err := model.ValidateDecision("crisis", model.Decision{Approve: true}); !errors.Is(err, model.ErrInvalidInput) {
		t.Errorf("unknown kind: got %v", err)
	}
}

func TestStaffRole(t *testing.T) {
	cases := []struct {
		roles []string
		want  string
	}{
		{nil, ""},
		{[]string{"psychologist"}, ""},
		{[]string{"lawyer"}, ""},
		{[]string{"moderator"}, "moderator"},
		{[]string{"admin"}, "admin"},
		{[]string{"moderator", "admin"}, "admin"},
		{[]string{"lawyer", "moderator"}, "moderator"},
	}
	for _, c := range cases {
		if got := model.StaffRole(c.roles); got != c.want {
			t.Errorf("StaffRole(%v) = %q, want %q", c.roles, got, c.want)
		}
	}
}

func TestSourceReasonKeepsOnlyCodes(t *testing.T) {
	for in, want := range map[string]string{
		"newcomer": "newcomer", "phone": "phone", "spam_2": "spam_2", "": "",
		"Call me at 555": "", "x y": "", "UPPER": "",
		"a_very_long_reason_code_that_exceeds_the_limit": "",
	} {
		if got := model.SourceReason(in); got != want {
			t.Errorf("SourceReason(%q) = %q, want %q", in, got, want)
		}
	}
}
