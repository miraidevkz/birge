package presets_test

import (
	"strings"
	"testing"

	"birge/internal/domains/safety/crisis"
	"birge/presets"
)

func TestCrisisLexiconLoads(t *testing.T) {
	fsys, err := presets.CrisisLexicon()
	if err != nil {
		t.Fatal(err)
	}
	lex, err := crisis.Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	langs := strings.Join(lex.Languages(), ",")
	if langs != "en,kk,ru" {
		t.Fatalf("languages = %q, want en,kk,ru", langs)
	}
	if !lex.Detect("want to die", "en").Crisis {
		t.Fatal("english DEMO entry did not match")
	}
}

func TestQuestionnaireMarkedExample(t *testing.T) {
	b, err := presets.Questionnaire()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "EXAMPLE") {
		t.Fatal("questionnaire missing EXAMPLE mark")
	}
}

func TestSMSTextsDemoPrefix(t *testing.T) {
	texts, err := presets.SMSTexts()
	if err != nil {
		t.Fatal(err)
	}
	for tmpl, byLang := range texts {
		for lang, body := range byLang {
			if !strings.HasPrefix(body, "[DEMO]") {
				t.Errorf("%s/%s missing [DEMO] prefix", tmpl, lang)
			}
		}
	}
	for _, tmpl := range []string{
		"safety.escalation_opened",
		"safety.escalation_unattended",
		"emergency.sos_raised",
		"emergency.sos_raised_contact",
		"emergency.sos_responded",
		"emergency.sos_shared",
		"emergency.trusted_invitation",
		"emergency.plan_sent",
		"wellbeing.checkin_reminder",
	} {
		if texts[tmpl] == nil {
			t.Errorf("missing template %s", tmpl)
		}
	}
}

func TestSettingsAndReference(t *testing.T) {
	s, err := presets.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.ConsentVersions["research"] != "demo-1" {
		t.Fatalf("consent = %v", s.ConsentVersions)
	}
	if len(s.CrisisPhones) != 1 || s.CrisisPhones[0].Number != "112" {
		t.Fatalf("phones = %+v", s.CrisisPhones)
	}
	ref, err := presets.LoadReference()
	if err != nil {
		t.Fatal(err)
	}
	if ref.Cities[0].Code != "demo_city" || len(ref.Needs) == 0 {
		t.Fatalf("reference = %+v", ref)
	}
}

func TestKnowledgeDemoMarked(t *testing.T) {
	k, err := presets.LoadKnowledge()
	if err != nil {
		t.Fatal(err)
	}
	if len(k.Articles) < 3 {
		t.Fatalf("want at least 3 demo articles, got %d", len(k.Articles))
	}
	langs := map[string]int{}
	for _, a := range k.Articles {
		for _, v := range a.Versions {
			if !strings.Contains(v.Title, "DEMO") || !strings.Contains(v.Body, "DEMO") {
				t.Errorf("version %s/%s missing DEMO mark", a.TopicCode, v.Lang)
			}
			langs[v.Lang]++
		}
	}
	for _, lang := range []string{"kk", "ru", "en"} {
		if langs[lang] < 3 {
			t.Errorf("lang %s has %d versions, want ≥3", lang, langs[lang])
		}
	}
}
