// Package presets embeds the DEMO configuration and reference data used when a
// deployment variable is unset, so a fresh binary starts without silent empty
// configuration. Every DEMO preset is marked DEMO or EXAMPLE in its file;
// wiring logs event=DemoPreset at Warn for each one in use. A real deployment
// must replace all of them (crisis lexicon and questionnaire thresholds come
// from the psychologist). Keys and secrets are never presets.
//
// Reference data (cities, districts, needs) is the exception: besides the
// DEMO set (demo/reference.json) the package carries real, versioned sets per
// pilot country (kz/reference.json). `birge migrate` syncs the database with
// the sets a deployment selects (BIRGE_REFERENCE_PRESETS); see LoadReference.
package presets

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

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
	NameAssistantPrompt = "assistant_prompt"
	NameDirectory       = "directory"
	NameListings        = "listings"
	NameAnalyticsK      = "analytics_k"
)

//go:embed demo kz
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

// Settings is the DEMO consent versions, crisis phones and metrics
// small-cell threshold from settings.json.
type Settings struct {
	ConsentVersions map[string]string    `json:"consent_versions"`
	CrisisPhones    []config.CrisisPhone `json:"crisis_phones"`
	// AnalyticsK is the small-cell threshold of a deployment running on these
	// DEMO settings (1: demo data shows numbers; ADR-0024).
	AnalyticsK int `json:"analytics_k"`
}

// LoadSettings reads the DEMO settings (consent versions, crisis phones, analytics k).
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
	if raw.AnalyticsK < 1 {
		return Settings{}, fmt.Errorf("presets: settings: analytics_k must be at least 1")
	}
	return raw, nil
}

// Reference set names (directories under presets/ holding reference.json).
const (
	// ReferenceDemo is the synthetic DEMO city with its districts and needs.
	ReferenceDemo = "demo"
	// ReferenceKZ is Kazakhstan: the cities of republican significance and the
	// regional centres, the districts of Almaty, Astana and Shymkent.
	ReferenceKZ = "kz"
)

// ReferenceSets lists the reference sets this binary carries.
var ReferenceSets = []string{ReferenceDemo, ReferenceKZ}

// DefaultReferenceSets is what migrate syncs when BIRGE_REFERENCE_PRESETS is
// unset: the DEMO set keeps a demo stand (and its members, rooms, cards and
// listings) working, and the pilot country is offered next to it.
var DefaultReferenceSets = []string{ReferenceDemo, ReferenceKZ}

// ReferenceLangs are the languages every reference name must have.
var ReferenceLangs = []string{"kk", "ru", "en"}

var referenceCodeRe = regexp.MustCompile(`^[a-z_]{2,40}$`)

// ReferenceEntry is one district or need: a stable code (the locale key and
// the source of its stable ID) and its names in kk, ru and en. The names are
// the reviewed source for the client locale files (namespace reference); the
// database stores codes only.
type ReferenceEntry struct {
	Code  string            `json:"code"`
	Names map[string]string `json:"names"`
}

// City is a city with its districts.
type City struct {
	Code      string            `json:"code"`
	Names     map[string]string `json:"names"`
	Districts []ReferenceEntry  `json:"districts"`
}

// Reference is one reference set (cities, districts, needs).
type Reference struct {
	Cities []City           `json:"cities"`
	Needs  []ReferenceEntry `json:"needs"`
}

// DistrictCodes returns every district code of the set, city by city.
func (r Reference) DistrictCodes() []string {
	var out []string
	for _, c := range r.Cities {
		for _, d := range c.Districts {
			out = append(out, d.Code)
		}
	}
	return out
}

// NeedCodes returns the needs codes of the set.
func (r Reference) NeedCodes() []string {
	out := make([]string, 0, len(r.Needs))
	for _, n := range r.Needs {
		out = append(out, n.Code)
	}
	return out
}

// LoadReference reads and validates the reference set name (one of
// ReferenceSets): valid and unique codes (district codes unique across the
// set's cities, because clients label them by code alone), every city with at
// least one district, and a non-empty name in every ReferenceLangs language
// for every entry.
func LoadReference(name string) (Reference, error) {
	if !slices.Contains(ReferenceSets, name) {
		return Reference{}, fmt.Errorf("presets: reference: unknown set %q", name)
	}
	b, err := demoFS.ReadFile(name + "/reference.json")
	if err != nil {
		return Reference{}, fmt.Errorf("presets: reference %s: %w", name, err)
	}
	var raw Reference
	if err := json.Unmarshal(b, &raw); err != nil {
		return Reference{}, fmt.Errorf("presets: reference %s: %w", name, err)
	}
	if err := raw.validate(); err != nil {
		return Reference{}, fmt.Errorf("presets: reference %s: %w", name, err)
	}
	return raw, nil
}

func (r Reference) validate() error {
	if len(r.Cities) == 0 || len(r.Needs) == 0 {
		return errors.New("cities and needs required")
	}
	cities, districts, needs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range r.Cities {
		if err := checkEntry("city", c.Code, c.Names, cities); err != nil {
			return err
		}
		if len(c.Districts) == 0 {
			return fmt.Errorf("city %s needs districts", c.Code)
		}
		for _, d := range c.Districts {
			if err := checkEntry("district", d.Code, d.Names, districts); err != nil {
				return err
			}
		}
	}
	for _, n := range r.Needs {
		if err := checkEntry("need", n.Code, n.Names, needs); err != nil {
			return err
		}
	}
	return nil
}

func checkEntry(kind, code string, names map[string]string, seen map[string]bool) error {
	if !referenceCodeRe.MatchString(code) {
		return fmt.Errorf("%s %q: invalid code", kind, code)
	}
	if seen[code] {
		return fmt.Errorf("%s %s: duplicate code", kind, code)
	}
	seen[code] = true
	for _, lang := range ReferenceLangs {
		if strings.TrimSpace(names[lang]) == "" {
			return fmt.Errorf("%s %s: missing %s name", kind, code, lang)
		}
	}
	return nil
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
	Slug      string             `json:"slug"`
	Versions  []KnowledgeVersion `json:"versions"`
}

// Knowledge is the DEMO knowledge base (topics and articles).
type Knowledge struct {
	Topics   []KnowledgeTopic   `json:"topics"`
	Articles []KnowledgeArticle `json:"articles"`
}

// AssistantPrompt returns the DEMO system prompt (not approved; see the file
// header). Version name is the file stem: assistant.v1.
func AssistantPrompt() (version string, text []byte, err error) {
	b, err := demoFS.ReadFile("demo/prompts/assistant.v1.md")
	if err != nil {
		return "", nil, fmt.Errorf("presets: assistant prompt: %w", err)
	}
	return "assistant.v1", b, nil
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
		if a.TopicCode == "" || a.Slug == "" || len(a.Versions) == 0 {
			return Knowledge{}, fmt.Errorf("presets: knowledge: each article needs topic_code, slug and versions")
		}
		for _, v := range a.Versions {
			if v.Lang == "" || v.Title == "" || v.Body == "" {
				return Knowledge{}, fmt.Errorf("presets: knowledge: each version needs lang, title and body")
			}
		}
	}
	return out, nil
}

// DemoSpecialist is one DEMO specialist card. Slug is a stable key that makes
// the seed idempotent; District and Needs are reference codes (no UUIDs;
// resolved on seed).
type DemoSpecialist struct {
	Slug       string   `json:"slug"`
	Name       string   `json:"name"`
	Profession string   `json:"profession"`
	District   string   `json:"district"`
	Needs      []string `json:"needs"`
	Contacts   string   `json:"contacts"`
}

// Directory is the DEMO specialist directory.
type Directory struct {
	Specialists []DemoSpecialist `json:"specialists"`
}

// LoadDirectory reads the DEMO specialists (names and contacts are synthetic
// and contain DEMO).
func LoadDirectory() (Directory, error) {
	b, err := demoFS.ReadFile("demo/directory.json")
	if err != nil {
		return Directory{}, fmt.Errorf("presets: directory: %w", err)
	}
	var raw Directory
	if err := json.Unmarshal(b, &raw); err != nil {
		return Directory{}, fmt.Errorf("presets: directory: %w", err)
	}
	if len(raw.Specialists) == 0 {
		return Directory{}, fmt.Errorf("presets: directory: specialists required")
	}
	seen := map[string]bool{}
	for _, s := range raw.Specialists {
		if s.Slug == "" || s.Name == "" || s.Profession == "" || s.District == "" {
			return Directory{}, fmt.Errorf("presets: directory: each specialist needs slug, name, profession and district")
		}
		if seen[s.Slug] {
			return Directory{}, fmt.Errorf("presets: directory: duplicate slug %s", s.Slug)
		}
		seen[s.Slug] = true
	}
	return raw, nil
}

// DemoListing is one DEMO item listing. Slug is a stable key that makes the
// seed idempotent; District is a reference code (resolved on seed).
type DemoListing struct {
	Slug        string `json:"slug"`
	Type        string `json:"type"`
	Category    string `json:"category"`
	District    string `json:"district"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Listings is the DEMO sharing feed.
type Listings struct {
	Listings []DemoListing `json:"listings"`
}

// LoadListings reads the DEMO listings (texts are synthetic and contain DEMO).
func LoadListings() (Listings, error) {
	b, err := demoFS.ReadFile("demo/listings.json")
	if err != nil {
		return Listings{}, fmt.Errorf("presets: listings: %w", err)
	}
	var raw Listings
	if err := json.Unmarshal(b, &raw); err != nil {
		return Listings{}, fmt.Errorf("presets: listings: %w", err)
	}
	if len(raw.Listings) == 0 {
		return Listings{}, fmt.Errorf("presets: listings: listings required")
	}
	seen := map[string]bool{}
	for _, l := range raw.Listings {
		if l.Slug == "" || l.Type == "" || l.Category == "" || l.District == "" || l.Title == "" || l.Description == "" {
			return Listings{}, fmt.Errorf("presets: listings: each listing needs slug, type, category, district, title and description")
		}
		if seen[l.Slug] {
			return Listings{}, fmt.Errorf("presets: listings: duplicate slug %s", l.Slug)
		}
		seen[l.Slug] = true
	}
	return raw, nil
}
