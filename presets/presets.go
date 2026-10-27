// Package presets embeds the DEMO configuration and reference data used when a
// deployment variable is unset, so a fresh binary starts without silent empty
// configuration. Every preset is marked DEMO or EXAMPLE in its file; wiring
// logs event=DemoPreset at Warn for each one in use. A real deployment must
// replace all of them (crisis lexicon and questionnaire thresholds come from
// the psychologist). Keys and secrets are never presets.
package presets

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"

	"birge/internal/platform/config"
)

// Preset names logged as event=DemoPreset name=<name>.
const (
	NameCrisisLexicon   = "crisis_lexicon"
	NameQuestionnaire   = "questionnaire"
	NameSMSTexts        = "sms_texts"
	NameConsentVersions = "consent_versions"
	NameCrisisPhones    = "crisis_phones"
	NameReference       = "reference"
	NameKnowledge       = "knowledge"
)

//go:embed demo
var demoFS embed.FS

// CrisisLexicon returns an fs.FS whose root holds the DEMO crisis lexicon
// files (<kk|ru|en>.<category>.txt).
func CrisisLexicon() (fs.FS, error) {
	sub, err := fs.Sub(demoFS, "demo/crisis")
	if err != nil {
		return nil, fmt.Errorf("presets: crisis lexicon: %w", err)
	}
	return sub, nil
}

// Questionnaire returns the EXAMPLE wellbeing questionnaire JSON.
func Questionnaire() ([]byte, error) {
	b, err := demoFS.ReadFile("demo/questionnaire.json")
	if err != nil {
		return nil, fmt.Errorf("presets: questionnaire: %w", err)
	}
	return b, nil
}

// SMSTexts returns the DEMO neutral SMS bodies (template → language → text).
func SMSTexts() (map[string]map[string]string, error) {
	b, err := demoFS.ReadFile("demo/sms_texts.json")
	if err != nil {
		return nil, fmt.Errorf("presets: sms texts: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("presets: sms texts: %w", err)
	}
	delete(raw, "_comment")
	out := make(map[string]map[string]string, len(raw))
	for k, v := range raw {
		var byLang map[string]string
		if err := json.Unmarshal(v, &byLang); err != nil {
			return nil, fmt.Errorf("presets: sms texts: template %s: %w", k, err)
		}
		out[k] = byLang
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("presets: sms texts: empty")
	}
	return out, nil
}

// Settings is the DEMO consent versions and crisis phones from settings.json.
type Settings struct {
	ConsentVersions map[string]string    `json:"consent_versions"`
	CrisisPhones    []config.CrisisPhone `json:"crisis_phones"`
}

// LoadSettings reads the DEMO settings (consent versions and crisis phones).
func LoadSettings() (Settings, error) {
	b, err := demoFS.ReadFile("demo/settings.json")
	if err != nil {
		return Settings{}, fmt.Errorf("presets: settings: %w", err)
	}
	var raw Settings
	if err := json.Unmarshal(b, &raw); err != nil {
		return Settings{}, fmt.Errorf("presets: settings: %w", err)
	}
	if len(raw.ConsentVersions) == 0 {
		return Settings{}, fmt.Errorf("presets: settings: consent_versions required")
	}
	if len(raw.CrisisPhones) == 0 {
		return Settings{}, fmt.Errorf("presets: settings: crisis_phones required")
	}
	return raw, nil
}

// City is a DEMO city with district codes (no UUIDs; assigned on seed).
type City struct {
	Code      string   `json:"code"`
	Districts []string `json:"districts"`
}

// Reference is the DEMO membership reference data (cities, districts, needs).
type Reference struct {
	Cities []City   `json:"cities"`
	Needs  []string `json:"needs"`
}

// LoadReference reads the DEMO city/district/needs codes.
func LoadReference() (Reference, error) {
	b, err := demoFS.ReadFile("demo/reference.json")
	if err != nil {
		return Reference{}, fmt.Errorf("presets: reference: %w", err)
	}
	var raw Reference
	if err := json.Unmarshal(b, &raw); err != nil {
		return Reference{}, fmt.Errorf("presets: reference: %w", err)
	}
	if len(raw.Cities) == 0 || len(raw.Needs) == 0 {
		return Reference{}, fmt.Errorf("presets: reference: cities and needs required")
	}
	for _, c := range raw.Cities {
		if c.Code == "" || len(c.Districts) == 0 {
			return Reference{}, fmt.Errorf("presets: reference: each city needs a code and districts")
		}
	}
	return raw, nil
}

// KnowledgeTopic is a DEMO knowledge topic (no UUIDs; assigned on seed).
type KnowledgeTopic struct {
	Kind   string            `json:"kind"`
	Code   string            `json:"code"`
	Titles map[string]string `json:"titles"`
}

// KnowledgeVersion is one DEMO language version of an article.
type KnowledgeVersion struct {
	Lang      string `json:"lang"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	SourceURL string `json:"source_url"`
}

// KnowledgeArticle is a DEMO article under a topic code.
type KnowledgeArticle struct {
	TopicCode string             `json:"topic_code"`
	Versions  []KnowledgeVersion `json:"versions"`
}

// Knowledge is the DEMO knowledge base (topics and articles).
type Knowledge struct {
	Topics   []KnowledgeTopic   `json:"topics"`
	Articles []KnowledgeArticle `json:"articles"`
}

// LoadKnowledge reads the DEMO knowledge articles (titles/bodies contain DEMO).
func LoadKnowledge() (Knowledge, error) {
	b, err := demoFS.ReadFile("demo/knowledge/articles.json")
	if err != nil {
		return Knowledge{}, fmt.Errorf("presets: knowledge: %w", err)
	}
	var raw struct {
		Topics   []KnowledgeTopic   `json:"topics"`
		Articles []KnowledgeArticle `json:"articles"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Knowledge{}, fmt.Errorf("presets: knowledge: %w", err)
	}
	if len(raw.Topics) == 0 || len(raw.Articles) == 0 {
		return Knowledge{}, fmt.Errorf("presets: knowledge: topics and articles required")
	}
	out := Knowledge{Topics: raw.Topics, Articles: raw.Articles}
	for _, t := range out.Topics {
		if t.Kind == "" || t.Code == "" || len(t.Titles) == 0 {
			return Knowledge{}, fmt.Errorf("presets: knowledge: each topic needs kind, code and titles")
		}
	}
	for _, a := range out.Articles {
		if a.TopicCode == "" || len(a.Versions) == 0 {
			return Knowledge{}, fmt.Errorf("presets: knowledge: each article needs topic_code and versions")
		}
		for _, v := range a.Versions {
			if v.Lang == "" || v.Title == "" || v.Body == "" {
				return Knowledge{}, fmt.Errorf("presets: knowledge: each version needs lang, title and body")
			}
		}
	}
	return out, nil
}
