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

func TestDirectoryAndListingsDemoMarked(t *testing.T) {
	ref, err := presets.LoadReference()
	if err != nil {
		t.Fatal(err)
	}
	districts := map[string]bool{}
	for _, c := range ref.Cities {
		for _, d := range c.Districts {
			districts[d] = true
		}
	}
	needs := map[string]bool{}
	for _, n := range ref.Needs {
		needs[n] = true
	}
	dir, err := presets.LoadDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(dir.Specialists); n < 6 || n > 8 {
		t.Fatalf("specialists = %d, want 6..8", n)
	}
	for _, s := range dir.Specialists {
		if !strings.Contains(s.Name, "DEMO") || !strings.Contains(s.Contacts, "DEMO") {
			t.Errorf("specialist %s missing DEMO mark", s.Slug)
		}
		if !strings.HasPrefix(s.Contacts, "+7000000000") {
			t.Errorf("specialist %s phone is not in the synthetic range: %q", s.Slug, s.Contacts)
		}
		if !districts[s.District] {
			t.Errorf("specialist %s district %q not in DEMO reference", s.Slug, s.District)
		}
		for _, n := range s.Needs {
			if !needs[n] {
				t.Errorf("specialist %s need %q not in DEMO reference", s.Slug, n)
			}
		}
	}
	ls, err := presets.LoadListings()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ls.Listings); n < 5 || n > 6 {
		t.Fatalf("listings = %d, want 5..6", n)
	}
	for _, l := range ls.Listings {
		if !strings.Contains(l.Title, "DEMO") || !strings.Contains(l.Description, "DEMO") {
			t.Errorf("listing %s missing DEMO mark", l.Slug)
		}
		if !districts[l.District] {
			t.Errorf("listing %s district %q not in DEMO reference", l.Slug, l.District)
		}
	}
}

// TestSMSTextsLinkOnlyInPlanSent: the {link} placeholder (a one-time plan
// link, notify model.SMSLinkPlaceholder) belongs only to the plan SMS of
// external contacts; every language of that text carries it exactly once.
func TestSMSTextsLinkOnlyInPlanSent(t *testing.T) {
	texts, err := presets.SMSTexts()
	if err != nil {
		t.Fatal(err)
	}
	for tmpl, byLang := range texts {
		for lang, body := range byLang {
			n := strings.Count(body, "{link}")
			if tmpl == "emergency.plan_sent" && n != 1 {
				t.Errorf("%s/%s: {link} %d times, want once", tmpl, lang, n)
			}
			if tmpl != "emergency.plan_sent" && n != 0 {
				t.Errorf("%s/%s: unexpected {link}", tmpl, lang)
			}
		}
	}
	for _, lang := range []string{"kk", "ru", "en"} {
		if texts["emergency.plan_sent"][lang] == "" {
			t.Errorf("emergency.plan_sent/%s missing", lang)
		}
	}
}
