package model_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/uuid"

	"birge/internal/domains/membership/model"
)

func TestNormalizeDisplayName(t *testing.T) {
	tests := []struct {
		in, want string
		err      error
	}{
		{"Example", "Example", nil},
		{"  Ғалия  ", "Ғалия", nil},
		{strings.Repeat("ж", 40), strings.Repeat("ж", 40), nil},
		{strings.Repeat("ж", 41), "", model.ErrInvalidDisplayName},
		{"", "", model.ErrInvalidDisplayName},
		{" \t ", "", model.ErrInvalidDisplayName},
		{"a\x00b", "", model.ErrInvalidDisplayName},
		{"a\u0085b", "", model.ErrInvalidDisplayName},
		{"a\xffb", "", model.ErrInvalidDisplayName},
	}
	for _, tt := range tests {
		got, err := model.NormalizeDisplayName(tt.in)
		if got != tt.want || !errors.Is(err, tt.err) {
			t.Errorf("NormalizeDisplayName(%q) = %q, %v; want %q, %v", tt.in, got, err, tt.want, tt.err)
		}
	}
}

func TestChildInputNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   model.ChildInput
		want []string
		err  error
	}{
		{"none", model.ChildInput{BirthYear: 2020}, []string{}, nil},
		{"dedup and sort", model.ChildInput{BirthYear: 1990, Needs: []string{"bb", "aa", "bb"}}, []string{"aa", "bb"}, nil},
		{"upper bound", model.ChildInput{BirthYear: 2100}, []string{}, nil},
		{"year low", model.ChildInput{BirthYear: 1989}, nil, model.ErrInvalidBirthYear},
		{"year high", model.ChildInput{BirthYear: 2101}, nil, model.ErrInvalidBirthYear},
		{"bad code", model.ChildInput{BirthYear: 2020, Needs: []string{"a"}}, nil, model.ErrInvalidNeeds},
		{"too many", model.ChildInput{BirthYear: 2020, Needs: make([]string, 11)}, nil, model.ErrInvalidNeeds},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.Normalize()
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if err == nil && strings.Join(got.Needs, ",") != strings.Join(tt.want, ",") {
				t.Errorf("needs = %v, want %v", got.Needs, tt.want)
			}
		})
	}
}

func TestProfileUpdateNormalize(t *testing.T) {
	name, bad, lang, badLang, nilID := " Example ", "", model.LangRU, model.Lang("de"), uuid.Nil
	tests := []struct {
		name string
		in   model.ProfileUpdate
		err  error
	}{
		{"empty", model.ProfileUpdate{}, model.ErrEmptyUpdate},
		{"name", model.ProfileUpdate{DisplayName: &name}, nil},
		{"bad name", model.ProfileUpdate{DisplayName: &bad}, model.ErrInvalidDisplayName},
		{"lang", model.ProfileUpdate{Lang: &lang}, nil},
		{"bad lang", model.ProfileUpdate{Lang: &badLang}, model.ErrInvalidLang},
		{"nil district", model.ProfileUpdate{DistrictID: &nilID}, model.ErrUnknownDistrict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.Normalize()
			if !errors.Is(err, tt.err) {
				t.Fatalf("error = %v, want %v", err, tt.err)
			}
			if err == nil && got.DisplayName != nil && *got.DisplayName != "Example" {
				t.Errorf("name = %q", *got.DisplayName)
			}
		})
	}
}

func TestMemberLogValueHidesPersonalData(t *testing.T) {
	var b strings.Builder
	slog.New(slog.NewTextHandler(&b, nil)).Info("member", "member", model.Member{ID: uuid.New(), DisplayName: "Example Name"})
	if strings.Contains(b.String(), "Example Name") {
		t.Fatalf("log line carries the display name: %s", b.String())
	}
}
