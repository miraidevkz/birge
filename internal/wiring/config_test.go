package wiring

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"

	"birge/presets"
)

// testKeysetJSON generates a fresh AES256-GCM Tink keyset; no key is ever
// written into the repository.
func testKeysetJSON(t *testing.T) []byte {
	t.Helper()
	h, err := keyset.NewHandle(aead.AES256GCMKeyTemplate())
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := insecurecleartextkeyset.Write(h, keyset.NewJSONWriter(&buf)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// randomKey returns a fresh key in the documented text form (base64 of 48
// random bytes, 64 characters), like `openssl rand -base64 48`.
func randomKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 48)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return []byte(base64.StdEncoding.EncodeToString(k))
}

// fakeFiles serves key files from memory.
type fakeFiles map[string][]byte

func (f fakeFiles) read(path string) ([]byte, error) {
	b, ok := f[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), b...), nil
}

const secretDatabaseURL = "postgres://birge:s3cret-password@db.example.invalid:5432/birge"

// testLexiconDir writes a minimal DEMO lexicon the loader accepts.
func testLexiconDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ru.suicide.txt"), []byte("хочу умереть\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// testQuestionnaireJSON is a minimal valid BIRGE_QUESTIONNAIRE_FILE body.
func testQuestionnaireJSON() []byte {
	return []byte(`{
  "version": "test-v1",
  "questions": [
    {"id":"phq_1","instrument":"phq4","text_key":"q.1","options":[{"value":0,"text_key":"o.0"},{"value":1,"text_key":"o.1"},{"value":2,"text_key":"o.2"},{"value":3,"text_key":"o.3"}]},
    {"id":"phq_2","instrument":"phq4","text_key":"q.2","options":[{"value":0,"text_key":"o.0"},{"value":1,"text_key":"o.1"},{"value":2,"text_key":"o.2"},{"value":3,"text_key":"o.3"}]},
    {"id":"phq_3","instrument":"phq4","text_key":"q.3","options":[{"value":0,"text_key":"o.0"},{"value":1,"text_key":"o.1"},{"value":2,"text_key":"o.2"},{"value":3,"text_key":"o.3"}]},
    {"id":"phq_4","instrument":"phq4","text_key":"q.4","options":[{"value":0,"text_key":"o.0"},{"value":1,"text_key":"o.1"},{"value":2,"text_key":"o.2"},{"value":3,"text_key":"o.3"}]},
    {"id":"pss_1","instrument":"pss4","text_key":"p.1","options":[{"value":0,"text_key":"po.0"},{"value":1,"text_key":"po.1"},{"value":2,"text_key":"po.2"},{"value":3,"text_key":"po.3"},{"value":4,"text_key":"po.4"}]},
    {"id":"pss_2","instrument":"pss4","text_key":"p.2","options":[{"value":0,"text_key":"po.0"},{"value":1,"text_key":"po.1"},{"value":2,"text_key":"po.2"},{"value":3,"text_key":"po.3"},{"value":4,"text_key":"po.4"}]},
    {"id":"pss_3","instrument":"pss4","text_key":"p.3","options":[{"value":0,"text_key":"po.0"},{"value":1,"text_key":"po.1"},{"value":2,"text_key":"po.2"},{"value":3,"text_key":"po.3"},{"value":4,"text_key":"po.4"}]},
    {"id":"pss_4","instrument":"pss4","text_key":"p.4","options":[{"value":0,"text_key":"po.0"},{"value":1,"text_key":"po.1"},{"value":2,"text_key":"po.2"},{"value":3,"text_key":"po.3"},{"value":4,"text_key":"po.4"}]}
  ],
  "reverse_score_question_ids": ["pss_2","pss_3"],
  "crisis_item_question_id": "phq_4",
  "thresholds": {"phq4":{"elevated_at":3,"crisis_at":9},"pss4":{"elevated_at":8,"crisis_at":13}},
  "feedback_by_flag": {
    "none":{"feedback_key":"wellbeing.feedback.none","suggestion":"practice"},
    "elevated":{"feedback_key":"wellbeing.feedback.elevated","suggestion":"psychologist"},
    "crisis":{"feedback_key":"wellbeing.feedback.crisis","suggestion":"none"}
  }
}`)
}

func validEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"DATABASE_URL":                secretDatabaseURL,
		"NATS_URL":                    "nats://nats.example.invalid:4222",
		"KRATOS_PUBLIC_URL":           "http://kratos.example.invalid:4433/",
		"KRATOS_ADMIN_URL":            "http://kratos.example.invalid:4434",
		"BIRGE_KEK_ID":                "local-1",
		"BIRGE_KEK_KEYSET_FILE":       "/keys/kek.json",
		"BIRGE_POW_KEY_FILE":          "/keys/pow",
		"BIRGE_HASH_KEY_FILE":         "/keys/hash",
		"BIRGE_PLAN_LINK_KEY_FILE":    "/keys/plan-link",
		"BIRGE_ANALYTICS_KEY_FILE":    "/keys/analytics",
		"BIRGE_PUBLIC_URL":            "https://app.example.invalid/",
		"BIRGE_SMS_ALLOWED_PREFIXES":  "+7700, +7701",
		"BIRGE_CONSENT_VERSIONS":      "data_processing=v1,ai_crossborder=v1,research=v2",
		"BIRGE_DELIVERY_MODE":         "dev",
		"BIRGE_CRISIS_LEXICON_DIR":    testLexiconDir(t),
		"BIRGE_QUESTIONNAIRE_FILE":    "/keys/questionnaire.json",
		"BIRGE_PRACTICES_BASE_URL":    "https://objects.example.invalid/practices",
		"BIRGE_CRISIS_PHONES":         `[{"number":"+77001112233","label":"Example line"}]`,
		"BIRGE_ASSISTANT_PROMPT_FILE": "/keys/assistant.v1.md",
	}
}

func validFiles(t *testing.T) fakeFiles {
	t.Helper()
	return fakeFiles{
		"/keys/kek.json":           testKeysetJSON(t),
		"/keys/pow":                append(randomKey(t), '\n'),
		"/keys/hash":               randomKey(t),
		"/keys/plan-link":          randomKey(t),
		"/keys/analytics":          randomKey(t),
		"/keys/questionnaire.json": testQuestionnaireJSON(),
		"/keys/assistant.v1.md":    []byte("# TEST assistant prompt\n\nReply in JSON only.\n"),
	}
}

func load(env map[string]string, files fakeFiles, cmd Command) (Config, error) {
	return Load(func(k string) string { return env[k] }, files.read, cmd)
}

func TestLoadValid(t *testing.T) {
	files := validFiles(t)
	for _, cmd := range []Command{CommandAPI, CommandWorker, CommandMigrate} {
		t.Run(string(cmd), func(t *testing.T) {
			cfg, err := load(validEnv(t), files, cmd)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DatabaseURL != secretDatabaseURL {
				t.Error("DATABASE_URL not loaded")
			}
			if cmd == CommandMigrate {
				if !slices.Equal(cfg.ReferencePresets, presets.DefaultReferenceSets) {
					t.Errorf("reference presets = %v", cfg.ReferencePresets)
				}
				return
			}
			if cfg.KEK == nil || cfg.KEK.ID() != "local-1" {
				t.Error("KEK not loaded")
			}
			if cfg.Lexicon == nil || cfg.EscalationBackupAfter != defaultEscalationBackup || cfg.StaffSMSLang != defaultStaffSMSLang {
				t.Errorf("safety defaults: lexicon=%v backup=%s lang=%q", cfg.Lexicon != nil, cfg.EscalationBackupAfter, cfg.StaffSMSLang)
			}
			if cfg.TimeZone != defaultTimeZone || cfg.KratosAdminURL == "" {
				t.Errorf("time zone / admin: %q %q", cfg.TimeZone, cfg.KratosAdminURL)
			}
			if cfg.DeletionDelay != 24*time.Hour || cfg.CriticalSMSAfter != 60*time.Second {
				t.Errorf("defaults: deletion %s, critical sms %s", cfg.DeletionDelay, cfg.CriticalSMSAfter)
			}
			if len(cfg.PlanLinkKey) < minKeyBytes {
				t.Error("plan link key not loaded")
			}
			if strings.Join(cfg.SMSPrefixes, ",") != "+7700,+7701" {
				t.Errorf("prefixes = %v", cfg.SMSPrefixes)
			}
			if cmd == CommandAPI {
				if len(cfg.PoWKey) != 64 {
					t.Errorf("PoW key length %d, want 64 (trailing newline trimmed)", len(cfg.PoWKey))
				}
				if cfg.KratosPublicURL != "http://kratos.example.invalid:4433" {
					t.Errorf("KratosPublicURL = %q", cfg.KratosPublicURL)
				}
				if cfg.ConsentVersions["research"] != "v2" || !cfg.APIPerIP.Valid() || cfg.AuthGlobal.Burst != 20 || cfg.PlanLinkPerIP.Burst != 10 {
					t.Error("API settings not loaded")
				}
				if cfg.Wellbeing.Questionnaire.Version != "test-v1" || cfg.PracticesBaseURL == "" {
					t.Error("wellbeing questionnaire / practices URL not loaded")
				}
				if cfg.AnalyticsK != 5 {
					t.Errorf("analytics k = %d, want the default 5 outside the DEMO preset", cfg.AnalyticsK)
				}
			}
			if cmd == CommandWorker {
				if cfg.NATSURL == "" || cfg.SMSGuard.DailyBudget != 1000 || cfg.PublicURL != "https://app.example.invalid" {
					t.Error("worker settings not loaded")
				}
				if cfg.InternalAddr != "" || cfg.CourierSecret != nil {
					t.Error("internal listener must be off unless BIRGE_INTERNAL_ADDR is set")
				}
				if cfg.EventsMaxAge != defaultEventsMaxAge || cfg.EventsMaxBytes != defaultEventsMaxBytes {
					t.Errorf("events retention: age %s, bytes %d", cfg.EventsMaxAge, cfg.EventsMaxBytes)
				}
				if len(cfg.AnalyticsKey) < minKeyBytes {
					t.Error("analytics key not loaded")
				}
			}
		})
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		cmd     Command
		set     map[string]string
		files   func(fakeFiles)
		wantErr string
	}{
		{"missing database", CommandMigrate, map[string]string{"DATABASE_URL": ""}, nil, "DATABASE_URL is required"},
		{"bad database", CommandMigrate, map[string]string{"DATABASE_URL": "postgres://birge:s3cret-password@[bad"}, nil, "DATABASE_URL: not a valid"},
		{"apply roles", CommandMigrate, map[string]string{"BIRGE_APPLY_ROLES": "yes"}, nil, "BIRGE_APPLY_ROLES"},
		{"unknown reference set", CommandMigrate, map[string]string{"BIRGE_REFERENCE_PRESETS": "kz,mars"}, nil, "BIRGE_REFERENCE_PRESETS: unknown or repeated"},
		{"repeated reference set", CommandMigrate, map[string]string{"BIRGE_REFERENCE_PRESETS": "kz,kz"}, nil, "BIRGE_REFERENCE_PRESETS: unknown or repeated"},
		{"missing kratos", CommandAPI, map[string]string{"KRATOS_PUBLIC_URL": ""}, nil, "KRATOS_PUBLIC_URL is required"},
		{"kratos with credentials", CommandAPI, map[string]string{"KRATOS_PUBLIC_URL": "http://u:p@kratos.example.invalid"}, nil, "KRATOS_PUBLIC_URL"},
		{"missing pow file", CommandAPI, nil, func(f fakeFiles) { delete(f, "/keys/pow") }, "BIRGE_POW_KEY_FILE: cannot read"},
		{"short key", CommandAPI, nil, func(f fakeFiles) { f["/keys/hash"] = []byte("short") }, "BIRGE_HASH_KEY_FILE: the key must be at least 32 bytes"},
		{"same keys", CommandAPI, nil, func(f fakeFiles) { f["/keys/hash"] = bytes.TrimSpace(f["/keys/pow"]) }, "must hold different keys"},
		{"missing plan link key", CommandWorker, map[string]string{"BIRGE_PLAN_LINK_KEY_FILE": ""}, nil, "BIRGE_PLAN_LINK_KEY_FILE is required"},
		{"plan link key is the hash key", CommandAPI, nil, func(f fakeFiles) { f["/keys/plan-link"] = f["/keys/hash"] }, "BIRGE_PLAN_LINK_KEY_FILE must hold its own key"},
		{"missing analytics key", CommandWorker, map[string]string{"BIRGE_ANALYTICS_KEY_FILE": ""}, nil, "BIRGE_ANALYTICS_KEY_FILE is required"},
		{"analytics key is the plan link key", CommandWorker, nil, func(f fakeFiles) { f["/keys/analytics"] = f["/keys/plan-link"] }, "BIRGE_ANALYTICS_KEY_FILE must hold its own key"},
		{"bad analytics k", CommandAPI, map[string]string{"BIRGE_ANALYTICS_K": "0"}, nil, "BIRGE_ANALYTICS_K"},
		{"missing public url", CommandWorker, map[string]string{"BIRGE_PUBLIC_URL": ""}, nil, "BIRGE_PUBLIC_URL is required"},
		{"bad plan link rate", CommandAPI, map[string]string{"BIRGE_PLAN_LINK_RATE_PER_IP": "x"}, nil, "BIRGE_PLAN_LINK_RATE_PER_IP"},
		{"bad keyset", CommandAPI, nil, func(f fakeFiles) { f["/keys/kek.json"] = []byte(`{"key":[]}`) }, "BIRGE_KEK_KEYSET_FILE"},
		{"bad kek id", CommandWorker, map[string]string{"BIRGE_KEK_ID": "Bad ID"}, nil, "BIRGE_KEK_ID/BIRGE_KEK_KEYSET_FILE: crypto: invalid KEK id"},
		{"bad prefix", CommandAPI, map[string]string{"BIRGE_SMS_ALLOWED_PREFIXES": "7700"}, nil, "BIRGE_SMS_ALLOWED_PREFIXES"},
		{"bad rate", CommandAPI, map[string]string{"BIRGE_API_RATE_PER_IP": "10"}, nil, "BIRGE_API_RATE_PER_IP"},
		{"zero rate", CommandAPI, map[string]string{"BIRGE_AUTH_RATE_GLOBAL": "0/1s"}, nil, "BIRGE_AUTH_RATE_GLOBAL"},
		{"missing consent type", CommandAPI, map[string]string{"BIRGE_CONSENT_VERSIONS": "data_processing=v1"}, nil, "missing a version for consent type research"},
		{"unknown consent type", CommandAPI, map[string]string{"BIRGE_CONSENT_VERSIONS": "marketing=v1"}, nil, "known consent types"},
		{"deletion too late", CommandWorker, map[string]string{"BIRGE_DELETION_DELAY": "48h"}, nil, "BIRGE_DELETION_DELAY"},
		{"critical sms too late", CommandWorker, map[string]string{"NOTIFY_CRITICAL_SMS_AFTER": "1h"}, nil, "NOTIFY_CRITICAL_SMS_AFTER"},
		{"bad proxy header", CommandAPI, map[string]string{"BIRGE_TRUSTED_PROXY_HEADER": "X Forwarded"}, nil, "BIRGE_TRUSTED_PROXY_HEADER"},
		{"missing nats", CommandWorker, map[string]string{"NATS_URL": ""}, nil, "NATS_URL is required"},
		{"bad nats", CommandWorker, map[string]string{"NATS_URL": "http://nats.example.invalid"}, nil, "NATS_URL"},
		{"no delivery mode", CommandWorker, map[string]string{"BIRGE_DELIVERY_MODE": ""}, nil, "BIRGE_DELIVERY_MODE is required"},
		{"unknown delivery mode", CommandWorker, map[string]string{"BIRGE_DELIVERY_MODE": "staging"}, nil, `must be "dev" or "production"`},
		{"sms texts required outside dev", CommandWorker, map[string]string{"BIRGE_DELIVERY_MODE": "production", "BIRGE_SMS_TEXTS": "{}"}, nil, "BIRGE_SMS_TEXTS is required"},
		{"bad sms texts", CommandWorker, map[string]string{"BIRGE_SMS_TEXTS": "[1]"}, nil, "BIRGE_SMS_TEXTS"},
		{"bad events max bytes", CommandWorker, map[string]string{"BIRGE_EVENTS_MAX_BYTES": "0"}, nil, "BIRGE_EVENTS_MAX_BYTES"},
		{"bad questionnaire", CommandAPI, nil, func(f fakeFiles) { f["/keys/questionnaire.json"] = []byte(`{}`) }, "BIRGE_QUESTIONNAIRE_FILE"},
		{"missing practices url", CommandAPI, map[string]string{"BIRGE_PRACTICES_BASE_URL": ""}, nil, "BIRGE_PRACTICES_BASE_URL is required"},
		{"bad staff lang", CommandAPI, map[string]string{"BIRGE_STAFF_SMS_LANG": "en"}, nil, "BIRGE_STAFF_SMS_LANG"},
		{"bad time zone", CommandWorker, map[string]string{"BIRGE_TIME_ZONE": "Not/AZone"}, nil, "BIRGE_TIME_ZONE"},
		{"internal listener without secret", CommandWorker, map[string]string{"BIRGE_INTERNAL_ADDR": ":8090"}, nil, "BIRGE_COURIER_SECRET_FILE is required"},
		{"short courier secret", CommandWorker, map[string]string{"BIRGE_INTERNAL_ADDR": ":8090", "BIRGE_COURIER_SECRET_FILE": "/keys/courier"},
			func(f fakeFiles) { f["/keys/courier"] = []byte("short") }, "BIRGE_COURIER_SECRET_FILE: the key must be at least 32 bytes"},
		{"bad internal addr", CommandWorker, map[string]string{"BIRGE_INTERNAL_ADDR": "8090"}, nil, "BIRGE_INTERNAL_ADDR"},
		{"internal addr on the health port", CommandWorker, map[string]string{"BIRGE_INTERNAL_ADDR": ":8080"}, nil, "another port than BIRGE_HTTP_ADDR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv(t)
			for k, v := range tt.set {
				env[k] = v
			}
			files := validFiles(t)
			if tt.files != nil {
				tt.files(files)
			}
			_, err := load(env, files, tt.cmd)
			if err == nil {
				t.Fatalf("Load accepted the configuration, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "s3cret-password") {
				t.Error("error message contains the database password")
			}
		})
	}
	t.Run("empty lexicon", func(t *testing.T) {
		env := validEnv(t)
		env["BIRGE_CRISIS_LEXICON_DIR"] = t.TempDir()
		_, err := load(env, validFiles(t), CommandWorker)
		if err == nil || !strings.Contains(err.Error(), "BIRGE_CRISIS_LEXICON_DIR") {
			t.Fatalf("error = %v, want BIRGE_CRISIS_LEXICON_DIR", err)
		}
	})
}

// All problems are reported at once, so an operator fixes them in one go.
func TestLoadReportsEveryProblem(t *testing.T) {
	_, err := load(map[string]string{}, fakeFiles{}, CommandWorker)
	if err == nil {
		t.Fatal("empty environment accepted")
	}
	for _, name := range []string{"DATABASE_URL", "NATS_URL", "KRATOS_ADMIN_URL", "BIRGE_KEK_ID", "BIRGE_SMS_ALLOWED_PREFIXES", "BIRGE_DELIVERY_MODE"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error does not mention %s: %v", name, err)
		}
	}
}

func TestDemoPresetsWhenUnset(t *testing.T) {
	files := validFiles(t)
	for _, cmd := range []Command{CommandAPI, CommandWorker} {
		t.Run(string(cmd), func(t *testing.T) {
			env := validEnv(t)
			delete(env, "BIRGE_CRISIS_LEXICON_DIR")
			delete(env, "BIRGE_QUESTIONNAIRE_FILE")
			delete(env, "BIRGE_SMS_TEXTS")
			delete(env, "BIRGE_CONSENT_VERSIONS")
			delete(env, "BIRGE_CRISIS_PHONES")
			delete(env, "BIRGE_ASSISTANT_PROMPT_FILE")
			cfg, err := load(env, files, cmd)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]bool{
				"crisis_lexicon": true,
				"crisis_phones":  true,
			}
			if cmd == CommandAPI {
				want["questionnaire"] = true
				want["consent_versions"] = true
				want["assistant_prompt"] = true
				want["analytics_k"] = true
			}
			if cmd == CommandWorker {
				want["sms_texts"] = true
			}
			got := map[string]bool{}
			for _, n := range cfg.DemoPresets {
				got[n] = true
			}
			for n := range want {
				if !got[n] {
					t.Errorf("missing DemoPreset %q in %v", n, cfg.DemoPresets)
				}
			}
			if len(cfg.DemoPresets) != len(want) {
				t.Errorf("DemoPresets = %v, want exactly %v", cfg.DemoPresets, want)
			}
			if cfg.Lexicon == nil || cfg.Lexicon.Size() == 0 {
				t.Error("demo lexicon not loaded")
			}
			if len(cfg.CrisisPhones) != 1 || cfg.CrisisPhones[0].Number != "112" {
				t.Errorf("demo crisis phones = %+v", cfg.CrisisPhones)
			}
			if cmd == CommandAPI {
				if cfg.ConsentVersions["data_processing"] != "demo-1" {
					t.Errorf("consent = %v", cfg.ConsentVersions)
				}
				if cfg.Wellbeing.Questionnaire.Version == "" {
					t.Error("demo questionnaire not loaded")
				}
				if cfg.AnalyticsK != 1 {
					t.Errorf("analytics k = %d, want 1 on the DEMO settings preset", cfg.AnalyticsK)
				}
			}
			if cmd == CommandWorker {
				if len(cfg.SMSTexts) == 0 || cfg.SMSTexts["safety.escalation_opened"]["en"] == "" {
					t.Errorf("demo sms texts = %v", cfg.SMSTexts)
				}
			}
			var buf strings.Builder
			log := slog.New(slog.NewJSONHandler(&buf, nil))
			LogDemoPresets(log, cfg.DemoPresets)
			for n := range want {
				if !strings.Contains(buf.String(), `"name":"`+n+`"`) || !strings.Contains(buf.String(), `"event":"DemoPreset"`) {
					t.Errorf("log missing DemoPreset %q: %s", n, buf.String())
				}
			}
		})
	}
}

func TestConfiguredValueReplacesDemoPreset(t *testing.T) {
	env := validEnv(t)
	files := validFiles(t)
	cfg, err := load(env, files, CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.DemoPresets) != 0 {
		t.Fatalf("DemoPresets = %v, want none when every variable is set", cfg.DemoPresets)
	}
	if cfg.CrisisPhones[0].Number == "112" {
		t.Fatal("configured crisis phones replaced by demo")
	}
	if cfg.Wellbeing.Questionnaire.Version != "test-v1" {
		t.Fatalf("questionnaire version = %q", cfg.Wellbeing.Questionnaire.Version)
	}
}

// TestAnalyticsK: an explicit BIRGE_ANALYTICS_K wins over the DEMO preset
// and adds no DemoPreset warning for it.
func TestAnalyticsK(t *testing.T) {
	for _, tt := range []struct {
		name   string
		demo   bool
		k      string
		want   int
		preset bool
	}{
		{"configured deployment, unset", false, "", 5, false},
		{"configured deployment, set", false, "10", 10, false},
		{"demo settings, unset", true, "", 1, true},
		{"demo settings, set", true, "3", 3, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv(t)
			if tt.demo {
				delete(env, "BIRGE_CONSENT_VERSIONS")
			}
			if tt.k != "" {
				env["BIRGE_ANALYTICS_K"] = tt.k
			}
			cfg, err := load(env, validFiles(t), CommandAPI)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.AnalyticsK != tt.want {
				t.Errorf("AnalyticsK = %d, want %d", cfg.AnalyticsK, tt.want)
			}
			if got := slices.Contains(cfg.DemoPresets, "analytics_k"); got != tt.preset {
				t.Errorf("analytics_k DemoPreset = %v, want %v (%v)", got, tt.preset, cfg.DemoPresets)
			}
		})
	}
}

func TestLoadNeverPrintsKeys(t *testing.T) {
	files := validFiles(t)
	files["/keys/hash"] = []byte("0123456789") // too short: rejected
	_, err := load(validEnv(t), files, CommandAPI)
	if err == nil || strings.Contains(err.Error(), "0123456789") {
		t.Fatalf("error = %v: want a rejection without the key", err)
	}
	cfg, err := load(validEnv(t), validFiles(t), CommandAPI)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg)} {
		if strings.Contains(s, "s3cret") || strings.Contains(s, "local-1") {
			t.Errorf("formatted config leaks settings: %q", s)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, err := load(validEnv(t), validFiles(t), "serve"); err == nil {
		t.Fatal("unknown command accepted")
	}
}

// staffEnv adds a complete Keycloak-style staff provider configuration.
func staffEnv(env map[string]string, files fakeFiles) {
	env["BIRGE_STAFF_INTROSPECTION_URL"] = "https://keycloak.example.invalid/realms/birge/protocol/openid-connect/token/introspect"
	env["BIRGE_STAFF_CLIENT_ID"] = "birge-api"
	env["BIRGE_STAFF_CLIENT_SECRET_FILE"] = "/keys/staff-secret"
	env["BIRGE_STAFF_ISSUER"] = "https://keycloak.example.invalid/realms/birge/"
	env["BIRGE_STAFF_AUDIENCE"] = "birge-admin"
	env["BIRGE_STAFF_ROLES_CLAIM"] = "realm_access.roles"
	env["BIRGE_STAFF_ROLE_MAP"] = "birge-psy=psychologist, birge-mod=moderator,birge-legal=lawyer,birge-admin=admin"
	env["BIRGE_STAFF_MFA_ACR"] = "2, 3"
	files["/keys/staff-secret"] = []byte("staff-secret-not-real\n")
}

func TestLoadStaffAuth(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		cfg, err := load(validEnv(t), validFiles(t), CommandAPI)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.StaffAuth != nil {
			t.Fatal("staff auth configured without BIRGE_STAFF_* variables")
		}
	})
	t.Run("complete", func(t *testing.T) {
		env, files := validEnv(t), validFiles(t)
		staffEnv(env, files)
		cfg, err := load(env, files, CommandAPI)
		if err != nil {
			t.Fatal(err)
		}
		s := cfg.StaffAuth
		if s == nil {
			t.Fatal("staff auth not loaded")
		}
		if s.ClientSecret != "staff-secret-not-real" {
			t.Error("client secret not read from the file (or newline kept)")
		}
		if s.Issuer != "https://keycloak.example.invalid/realms/birge/" {
			t.Errorf("issuer not kept verbatim: %q", s.Issuer)
		}
		if s.RoleMap["birge-psy"] != "psychologist" || s.RoleMap["birge-legal"] != "lawyer" || len(s.RoleMap) != 4 {
			t.Errorf("role map = %v", s.RoleMap)
		}
		if strings.Join(s.MFAACR, ",") != "2,3" || len(s.MFAAMR) != 0 || s.RolesClaim != "realm_access.roles" {
			t.Errorf("mfa / claim: acr %v amr %v claim %q", s.MFAACR, s.MFAAMR, s.RolesClaim)
		}
		for _, f := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg)} {
			if strings.Contains(f, "staff-secret-not-real") {
				t.Error("formatted config leaks the client secret")
			}
		}
	})
	t.Run("worker ignores it", func(t *testing.T) {
		env, files := validEnv(t), validFiles(t)
		staffEnv(env, files)
		cfg, err := load(env, files, CommandWorker)
		if err != nil || cfg.StaffAuth != nil {
			t.Fatalf("worker: %v, staff auth %v", err, cfg.StaffAuth != nil)
		}
	})
	rejects := []struct {
		name    string
		set     map[string]string
		files   func(fakeFiles)
		wantErr []string
	}{
		{"partial", map[string]string{"BIRGE_STAFF_INTROSPECTION_URL": "", "BIRGE_STAFF_ISSUER": "", "BIRGE_STAFF_CLIENT_SECRET_FILE": ""}, nil,
			[]string{"BIRGE_STAFF_INTROSPECTION_URL is required", "BIRGE_STAFF_ISSUER is required", "BIRGE_STAFF_CLIENT_SECRET_FILE is required"}},
		{"no mfa rule", map[string]string{"BIRGE_STAFF_MFA_ACR": " , "}, nil, []string{"BIRGE_STAFF_MFA_ACR or BIRGE_STAFF_MFA_AMR is required"}},
		{"unknown role", map[string]string{"BIRGE_STAFF_ROLE_MAP": "x=root"}, nil, []string{"BIRGE_STAFF_ROLE_MAP"}},
		{"role map syntax", map[string]string{"BIRGE_STAFF_ROLE_MAP": "admin"}, nil, []string{"BIRGE_STAFF_ROLE_MAP"}},
		{"issuer not a url", map[string]string{"BIRGE_STAFF_ISSUER": "keycloak"}, nil, []string{"BIRGE_STAFF_ISSUER"}},
		{"introspection credentials in url", map[string]string{"BIRGE_STAFF_INTROSPECTION_URL": "https://u:p@idp.example.invalid/introspect"}, nil, []string{"BIRGE_STAFF_INTROSPECTION_URL"}},
		{"missing secret file", nil, func(f fakeFiles) { delete(f, "/keys/staff-secret") }, []string{"BIRGE_STAFF_CLIENT_SECRET_FILE: cannot read"}},
		{"empty secret file", nil, func(f fakeFiles) { f["/keys/staff-secret"] = []byte(" \n") }, []string{"BIRGE_STAFF_CLIENT_SECRET_FILE: the secret file is empty"}},
		{"bad claim path", map[string]string{"BIRGE_STAFF_ROLES_CLAIM": "a..b"}, nil, []string{"BIRGE_STAFF_*", "RolesClaim"}},
		{"bad mfa value", map[string]string{"BIRGE_STAFF_MFA_AMR": `web"authn`}, nil, []string{"BIRGE_STAFF_*", "MFA values"}},
	}
	for _, tt := range rejects {
		t.Run(tt.name, func(t *testing.T) {
			env, files := validEnv(t), validFiles(t)
			staffEnv(env, files)
			for k, v := range tt.set {
				env[k] = v
			}
			if tt.files != nil {
				tt.files(files)
			}
			_, err := load(env, files, CommandAPI)
			if err == nil {
				t.Fatalf("accepted, want %v", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "staff-secret-not-real") {
				t.Error("error contains the client secret")
			}
		})
	}
}

func TestLoadReferencePresets(t *testing.T) {
	env := validEnv(t)
	env["BIRGE_REFERENCE_PRESETS"] = " kz "
	cfg, err := load(env, validFiles(t), CommandMigrate)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.ReferencePresets, []string{presets.ReferenceKZ}) {
		t.Fatalf("reference presets = %v", cfg.ReferencePresets)
	}
}
