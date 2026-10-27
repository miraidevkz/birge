package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/model"
)

func TestParseOutput(t *testing.T) {
	good := `{"reply":"ok","crisis":false,"sources":[],"suggest":"none"}`
	if out, err := model.ParseOutput(good); err != nil || out.Reply != "ok" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	bad := []string{
		``, `null`, `[]`, `{"reply":"ok"}`,
		`{"reply":1,"crisis":false,"sources":[],"suggest":"none"}`,
		`{"reply":"ok","crisis":"no","sources":[],"suggest":"none"}`,
		`{"reply":"ok","crisis":false,"sources":null,"suggest":"none"}`,
		`{"reply":"ok","crisis":false,"sources":[],"suggest":"none","x":1}`,
		good + ` trailing`,
	}
	for _, b := range bad {
		if _, err := model.ParseOutput(b); !errors.Is(err, model.ErrInvalidOutput) {
			t.Errorf("ParseOutput(%q) err = %v", b, err)
		}
	}
}

func TestValidate(t *testing.T) {
	a := model.Fragment{ArticleID: uuid.New()}
	b := model.Fragment{ArticleID: uuid.New()}
	out := model.Output{Reply: "ok", Suggest: model.SuggestTea, Sources: []string{b.ArticleID.String(), uuid.NewString(), "x", b.ArticleID.String(), a.ArticleID.String()}}
	kept, err := model.Validate(out, []model.Fragment{a, b})
	if err != nil || len(kept) != 2 || kept[0].ArticleID != b.ArticleID || kept[1].ArticleID != a.ArticleID {
		t.Fatalf("kept=%+v err=%v", kept, err)
	}
	for _, o := range []model.Output{
		{Reply: "", Suggest: model.SuggestNone},
		{Reply: strings.Repeat("ә", model.MaxReplyChars+1), Suggest: model.SuggestNone},
		{Reply: "ok", Suggest: "medicine"},
		{Reply: "\xff", Suggest: model.SuggestNone},
	} {
		if _, err := model.Validate(o, nil); !errors.Is(err, model.ErrInvalidOutput) {
			t.Errorf("Validate(%+v) err = %v", o, err)
		}
	}
	if _, err := model.Validate(model.Output{Reply: strings.Repeat("ә", model.MaxReplyChars), Suggest: model.SuggestNone}, nil); err != nil {
		t.Fatalf("2000 characters must pass: %v", err)
	}
}
