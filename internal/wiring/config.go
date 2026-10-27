package wiring

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/membership/authgw"
	"birge/internal/domains/notify/sms/guard"
	privacymodel "birge/internal/domains/privacy/model"
	"birge/internal/domains/safety/crisis"
	safetymodel "birge/internal/domains/safety/model"
	wellbeingmodel "birge/internal/domains/wellbeing/model"
	wellbeingservice "birge/internal/domains/wellbeing/service"
	"birge/internal/platform/config"
	"birge/internal/platform/crypto"
	"birge/internal/platform/ratelimit"
	"birge/presets"
)

// Command is a subcommand of the birge binary; it decides which settings are required.
type Command string

// Subcommands.
const (
	CommandAPI     Command = "api"
	CommandWorker  Command = "worker"
	CommandMigrate Command = "migrate"
)

// DeliveryModeDev enables the log-only push and SMS senders (development only).
const DeliveryModeDev = "dev"

// Upper bounds that keep a typo from disabling a guarantee.
const (
	// maxDeletionDelay: account deletion completes within 24 hours (requirements, ADR-0009).
	maxDeletionDelay = 24 * time.Hour
	// maxCriticalSMSAfter: the critical SMS fallback must not wait longer than
	// the 15-minute escalation backup.
	maxCriticalSMSAfter = 15 * time.Minute
	minKeyBytes         = 32
	// Default JetStream EVENTS retention: 30 days / 1 GiB.
	defaultEventsMaxAge   = 720 * time.Hour
	defaultEventsMaxBytes = int64(1 << 30)
	maxEventsMaxAge       = 8760 * time.Hour // 1 year
	// Escalation backup: requirements § 5; allow shorter values in tests.
	defaultEscalationBackup = 15 * time.Minute
	maxEscalationBackup     = time.Hour
	defaultStaffSMSLang     = "ru"
	defaultTimeZone         = "Asia/Almaty"
)

// Config is everything a subcommand needs, read from the environment by Load.
// Key material is read from files named in the environment, never from the
// environment itself. Config holds secrets (keys, the database URL with its
// password): never log it; String redacts it.
type Config struct {
	config.Config

	DatabaseURL     string
	NATSURL         string
	KratosPublicURL string
	KratosAdminURL  string

	// KEK wraps member data keys (ADR-0009); loaded from BIRGE_KEK_KEYSET_FILE.
	KEK *crypto.LocalKEK
	// BlindIndexKey is reserved for blind indexes (crypto.BlindIndexer); no
	// context uses one yet, so it is optional.
	BlindIndexKey []byte
	PoWKey        []byte
	HashKey       []byte
	PoWMaxNumber  int64

	// SMSPrefixes are the allowed E.164 mobile prefixes, shared by the auth
	// gateway and the notify SMS guard.
	SMSPrefixes []string
	SMSGuard    guard.Config
	// SMSTexts maps template -> language -> neutral fixed SMS body (ADR-0013).
	SMSTexts map[string]map[string]string

	AuthPerIP, AuthPerNumber, AuthGlobal authgw.Rate
	APIPerIP                             ratelimit.Rate

	// TrustedProxyHeader, when set, names the header whose right-most value
	// is the client IP (set by the ingress); otherwise RemoteAddr is used.
	TrustedProxyHeader string

	ConsentVersions  map[string]string
	DeletionDelay    time.Duration
	CriticalSMSAfter time.Duration
	DeliveryMode     string
	ApplyRoles       bool

	// EventsMaxAge / EventsMaxBytes bound JetStream EVENTS stream retention
	// (CreateOrUpdateStream on worker start).
	EventsMaxAge   time.Duration
	EventsMaxBytes int64

	// Safety (ADR-0023): lexicon is loaded at startup; empty or unloadable refuses to start.
	// When BIRGE_CRISIS_LEXICON_DIR is unset, Lexicon comes from the embedded DEMO preset.
	CrisisLexiconDir      string
	Lexicon               *crisis.Lexicon
	EscalationBackupAfter time.Duration
	StaffSMSLang          string

	// Wellbeing: questionnaire from BIRGE_QUESTIONNAIRE_FILE or the EXAMPLE preset.
	Wellbeing         wellbeingservice.Settings
	QuestionnaireFile string
	TimeZone          string
	PracticesBaseURL  string

	// DemoPresets lists embedded presets in use (logged once at Warn on start).
	DemoPresets []string
}

// String redacts the configuration so that an accidental %v cannot print secrets.
func (Config) String() string { return "wiring.Config{redacted}" }

// GoString redacts the configuration for %#v.
func (Config) GoString() string { return "wiring.Config{redacted}" }

// Load reads the configuration of cmd from getenv and readFile (os.Getenv and
// os.ReadFile in production). It reports every problem at once, naming the
// variables but never their values.
func Load(getenv func(string) string, readFile func(string) ([]byte, error), cmd Command) (Config, error) {
	base, err := config.Load(getenv)
	if err != nil {
		return Config{}, fmt.Errorf("configuration: %w", err)
	}
	l := loader{getenv: getenv, readFile: readFile}
	cfg := Config{Config: base}
	api, worker := cmd == CommandAPI, cmd == CommandWorker

	cfg.DatabaseURL = l.databaseURL("DATABASE_URL")
	cfg.ApplyRoles = l.flag("BIRGE_APPLY_ROLES")
	if cmd == CommandMigrate {
		return cfg, l.result()
	}
	if !api && !worker {
		return Config{}, fmt.Errorf("configuration: unknown command %q", cmd)
	}

	cfg.KEK = l.kek("BIRGE_KEK_ID", "BIRGE_KEK_KEYSET_FILE")
	cfg.BlindIndexKey = l.keyFile("BIRGE_BLIND_INDEX_KEY_FILE", false)
	cfg.SMSPrefixes = l.prefixes("BIRGE_SMS_ALLOWED_PREFIXES")
	cfg.DeletionDelay = l.duration("BIRGE_DELETION_DELAY", 24*time.Hour, maxDeletionDelay)
	cfg.CriticalSMSAfter = l.duration("NOTIFY_CRITICAL_SMS_AFTER", 60*time.Second, maxCriticalSMSAfter)
	cfg.TrustedProxyHeader = l.headerName("BIRGE_TRUSTED_PROXY_HEADER")
	cfg.CrisisLexiconDir = strings.TrimSpace(getenv("BIRGE_CRISIS_LEXICON_DIR"))
	cfg.Lexicon = l.lexiconOrDemo(&cfg)
	cfg.EscalationBackupAfter = l.duration("BIRGE_ESCALATION_BACKUP_AFTER", defaultEscalationBackup, maxEscalationBackup)
	cfg.StaffSMSLang = l.lang("BIRGE_STAFF_SMS_LANG", defaultStaffSMSLang)
	cfg.TimeZone = l.timeZone("BIRGE_TIME_ZONE", defaultTimeZone)
	// Kratos admin is needed on the API for staff contact reveal (MemberDirectory)
	// and on the worker for member SMS PhoneFor.
	cfg.KratosAdminURL = l.httpURL("KRATOS_ADMIN_URL", true)
	l.crisisPhonesOrDemo(&cfg)

	if api {
		cfg.KratosPublicURL = l.httpURL("KRATOS_PUBLIC_URL", true)
		cfg.PoWKey = l.keyFile("BIRGE_POW_KEY_FILE", true)
		cfg.HashKey = l.keyFile("BIRGE_HASH_KEY_FILE", true)
		if cfg.PoWKey != nil && bytes.Equal(cfg.PoWKey, cfg.HashKey) {
			l.fail("BIRGE_POW_KEY_FILE and BIRGE_HASH_KEY_FILE must hold different keys")
		}
		cfg.PoWMaxNumber = int64(l.positiveInt("BIRGE_AUTH_POW_MAX_NUMBER", 100_000))
		cfg.AuthPerIP = authgw.Rate(l.rate("BIRGE_AUTH_RATE_PER_IP", "20/1m"))
		cfg.AuthPerNumber = authgw.Rate(l.rate("BIRGE_AUTH_RATE_PER_NUMBER", "3/10m"))
		cfg.AuthGlobal = authgw.Rate(l.rate("BIRGE_AUTH_RATE_GLOBAL", "20/1s"))
		cfg.APIPerIP = l.rate("BIRGE_API_RATE_PER_IP", "300/100ms")
		cfg.ConsentVersions = l.consentVersionsOrDemo(&cfg)
		cfg.QuestionnaireFile = strings.TrimSpace(getenv("BIRGE_QUESTIONNAIRE_FILE"))
		cfg.Wellbeing = l.questionnaireOrDemo(&cfg)
		cfg.PracticesBaseURL = l.httpURL("BIRGE_PRACTICES_BASE_URL", true)
		cfg.Wellbeing.PracticesBaseURL = cfg.PracticesBaseURL
		cfg.Wellbeing.TimeZone = cfg.TimeZone
		cfg.Wellbeing.CrisisPhones = wellbeingCrisisPhones(cfg.CrisisPhones)
	}
	if worker {
		cfg.NATSURL = l.natsURL("NATS_URL")
		cfg.SMSGuard = guard.Config{
			AllowedPrefixes: cfg.SMSPrefixes,
			PerNumberLimit:  l.positiveInt("BIRGE_SMS_PER_NUMBER_LIMIT", 5),
			PerNumberWindow: l.duration("BIRGE_SMS_PER_NUMBER_WINDOW", time.Hour, 24*time.Hour),
			GlobalLimit:     l.positiveInt("BIRGE_SMS_GLOBAL_LIMIT", 60),
			GlobalWindow:    l.duration("BIRGE_SMS_GLOBAL_WINDOW", time.Minute, 24*time.Hour),
			DailyBudget:     l.positiveInt("BIRGE_SMS_DAILY_BUDGET", 1000),
		}
		cfg.SMSTexts = l.smsTextsOrDemo(&cfg)
		cfg.EventsMaxAge = l.duration("BIRGE_EVENTS_MAX_AGE", defaultEventsMaxAge, maxEventsMaxAge)
		cfg.EventsMaxBytes = l.positiveInt64("BIRGE_EVENTS_MAX_BYTES", defaultEventsMaxBytes)
		// Reminder scheduling needs the deployment zone; questionnaire is API-only.
		cfg.Wellbeing.TimeZone = cfg.TimeZone
		cfg.Wellbeing.CrisisPhones = wellbeingCrisisPhones(cfg.CrisisPhones)
		// No real push or SMS provider exists yet. Dropping notifications
		// silently is worse than not starting, so only the explicit
		// development mode may run with the log-only senders.
		switch cfg.DeliveryMode = getenv("BIRGE_DELIVERY_MODE"); cfg.DeliveryMode {
		case DeliveryModeDev:
		case "":
			l.fail("BIRGE_DELIVERY_MODE: no push or SMS provider is configured; set BIRGE_DELIVERY_MODE=dev to use the log-only senders in development")
		default:
			l.fail("BIRGE_DELIVERY_MODE: only \"dev\" is supported until real push and SMS providers exist")
		}
		// Outside development, SMS bodies must be configured: an empty map
		// would skip every SMS, including critical ones. Unset falls back to
		// the DEMO preset above; an explicit empty object still fails here.
		if cfg.DeliveryMode != DeliveryModeDev && len(cfg.SMSTexts) == 0 {
			l.fail("BIRGE_SMS_TEXTS is required when BIRGE_DELIVERY_MODE is not dev")
		}
	}
	if err := l.result(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LogDemoPresets emits one Warn line per embedded preset in use. Call once at
// process start; /readyz stays healthy regardless.
func LogDemoPresets(logger *slog.Logger, names []string) {
	for _, name := range names {
		logger.Warn("demo preset in use", "event", "DemoPreset", "name", name)
	}
}

// loader collects problems so that Load can report all of them at once.
type loader struct {
	getenv     func(string) string
	readFile   func(string) ([]byte, error)
	errs       []error
	settings   presets.Settings
	settingsOK bool
}

func (l *loader) fail(msg string) { l.errs = append(l.errs, errors.New(msg)) }

func (l *loader) result() error {
	if len(l.errs) == 0 {
		return nil
	}
	return fmt.Errorf("configuration:\n%w", errors.Join(l.errs...))
}

func (l *loader) required(name string) string {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		l.fail(name + " is required")
	}
	return v
}

func (l *loader) databaseURL(name string) string {
	v := l.required(name)
	if v == "" {
		return ""
	}
	// The parse error may quote the connection string, which can hold a
	// password: report only the variable name.
	if _, err := pgxpool.ParseConfig(v); err != nil {
		l.fail(name + ": not a valid PostgreSQL connection string")
		return ""
	}
	return v
}

func (l *loader) httpURL(name string, required bool) string {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		if required {
			l.fail(name + " is required")
		}
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		l.fail(name + ": must be an absolute http(s) URL without credentials")
		return ""
	}
	return strings.TrimRight(v, "/")
}

func (l *loader) natsURL(name string) string {
	v := l.required(name)
	if v == "" {
		return ""
	}
	// nats.Connect accepts a comma-separated list of servers.
	for _, s := range strings.Split(v, ",") {
		u, err := url.Parse(strings.TrimSpace(s))
		if err != nil || u.Host == "" || (u.Scheme != "nats" && u.Scheme != "tls") {
			l.fail(name + ": must be nats:// or tls:// URLs, comma-separated")
			return ""
		}
	}
	return v
}

func (l *loader) flag(name string) bool {
	switch l.getenv(name) {
	case "", "0":
		return false
	case "1":
		return true
	default:
		l.fail(name + ": must be 0 or 1")
		return false
	}
}

// keyFile reads a key from the file named by name. Keys are text (for
// example base64): trailing whitespace is removed, so a key written with a
// final newline works, and at least 32 bytes must remain.
func (l *loader) keyFile(name string, required bool) []byte {
	path := strings.TrimSpace(l.getenv(name))
	if path == "" {
		if required {
			l.fail(name + " is required (path to a key file)")
		}
		return nil
	}
	b, err := l.readFile(path)
	if err != nil {
		l.fail(name + ": cannot read the key file")
		return nil
	}
	key := bytes.TrimRight(b, " \t\r\n")
	if len(key) < minKeyBytes {
		clear(b)
		l.fail(fmt.Sprintf("%s: the key must be at least %d bytes", name, minKeyBytes))
		return nil
	}
	return key
}

func (l *loader) kek(idName, fileName string) *crypto.LocalKEK {
	id := l.required(idName)
	path := strings.TrimSpace(l.getenv(fileName))
	if path == "" {
		l.fail(fileName + " is required (path to a Tink AES256-GCM keyset in JSON)")
		return nil
	}
	if id == "" {
		return nil
	}
	b, err := l.readFile(path)
	if err != nil {
		l.fail(fileName + ": cannot read the keyset file")
		return nil
	}
	defer clear(b)
	k, err := crypto.NewLocalKEK(id, b)
	if err != nil {
		// crypto errors never carry key material; the error says whether the
		// id or the keyset is wrong.
		l.fail(idName + "/" + fileName + ": " + err.Error())
		return nil
	}
	return k
}

func (l *loader) prefixes(name string) []string {
	v := l.required(name)
	if v == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if len(p) < 2 || p[0] != '+' || strings.Trim(p[1:], "0123456789") != "" {
			l.fail(name + ": each prefix must be '+' followed by digits, comma-separated")
			return nil
		}
		out = append(out, p)
	}
	return out
}

func (l *loader) duration(name string, def, maxValue time.Duration) time.Duration {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 || d > maxValue {
		l.fail(fmt.Sprintf("%s: must be a duration above 0 and at most %s, e.g. 90s", name, maxValue))
		return def
	}
	return d
}

func (l *loader) positiveInt(name string, def int) int {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		l.fail(name + ": must be a positive integer")
		return def
	}
	return n
}

func (l *loader) positiveInt64(name string, def int64) int64 {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		l.fail(name + ": must be a positive integer (bytes)")
		return def
	}
	return n
}

// rate parses "<burst>/<interval>": burst tokens, one more every interval.
func (l *loader) rate(name, def string) ratelimit.Rate {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		v = def
	}
	b, e, ok := strings.Cut(v, "/")
	burst, err1 := strconv.Atoi(b)
	every, err2 := time.ParseDuration(e)
	r := ratelimit.Rate{Burst: burst, Every: every}
	if !ok || err1 != nil || err2 != nil || !r.Valid() {
		l.fail(name + ": must be <burst>/<interval>, e.g. 10/1m")
		return ratelimit.Rate{}
	}
	return r
}

func (l *loader) headerName(name string) string {
	v := strings.TrimSpace(l.getenv(name))
	for i := 0; i < len(v); i++ {
		if !isHeaderNameChar(v[i]) {
			l.fail(name + ": must be an HTTP header name, e.g. X-Forwarded-For")
			return ""
		}
	}
	return v
}

// isHeaderNameChar is the subset of RFC 9110 token characters used in real
// header names; anything else is a configuration mistake.
func isHeaderNameChar(c byte) bool {
	return c == '-' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// consentVersions parses "type=version,..." and requires every known consent type.
func (l *loader) consentVersions(name, v string) map[string]string {
	if v == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(v, ",") {
		t, ver, ok := strings.Cut(strings.TrimSpace(pair), "=")
		t, ver = strings.TrimSpace(t), strings.TrimSpace(ver)
		if !ok || ver == "" || !privacymodel.ConsentTypeIsValid(t) {
			l.fail(name + ": must be <type>=<version>, comma-separated, with known consent types")
			return nil
		}
		out[t] = ver
	}
	for _, t := range privacymodel.AllConsentTypes {
		if out[t] == "" {
			l.fail(name + ": missing a version for consent type " + t)
		}
	}
	return out
}

func (l *loader) consentVersionsOrDemo(cfg *Config) map[string]string {
	v := strings.TrimSpace(l.getenv("BIRGE_CONSENT_VERSIONS"))
	if v != "" {
		return l.consentVersions("BIRGE_CONSENT_VERSIONS", v)
	}
	s, err := l.demoSettings()
	if err != nil {
		l.fail("BIRGE_CONSENT_VERSIONS: " + err.Error())
		return nil
	}
	out := l.consentVersions("presets/"+presets.NameConsentVersions, formatConsentVersions(s.ConsentVersions))
	if out != nil {
		cfg.DemoPresets = append(cfg.DemoPresets, presets.NameConsentVersions)
	}
	return out
}

func formatConsentVersions(m map[string]string) string {
	parts := make([]string, 0, len(privacymodel.AllConsentTypes))
	for _, t := range privacymodel.AllConsentTypes {
		parts = append(parts, t+"="+m[t])
	}
	return strings.Join(parts, ",")
}

// smsTexts parses the JSON object {"<template>": {"<lang>": "<text>"}}.
func (l *loader) smsTexts(name, v string) map[string]map[string]string {
	if v == "" {
		return nil
	}
	var out map[string]map[string]string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		l.fail(name + `: must be a JSON object {"<template>": {"<lang>": "<text>"}}`)
		return nil
	}
	return out
}

func (l *loader) smsTextsOrDemo(cfg *Config) map[string]map[string]string {
	v := strings.TrimSpace(l.getenv("BIRGE_SMS_TEXTS"))
	if v != "" {
		return l.smsTexts("BIRGE_SMS_TEXTS", v)
	}
	texts, err := presets.SMSTexts()
	if err != nil {
		l.fail("BIRGE_SMS_TEXTS: " + err.Error())
		return nil
	}
	cfg.DemoPresets = append(cfg.DemoPresets, presets.NameSMSTexts)
	return texts
}

func (l *loader) lexiconOrDemo(cfg *Config) *crisis.Lexicon {
	if cfg.CrisisLexiconDir != "" {
		lex, err := crisis.LoadDir(cfg.CrisisLexiconDir)
		if err != nil {
			// LoadDir errors name the directory/file, never message text.
			l.fail("BIRGE_CRISIS_LEXICON_DIR: " + err.Error())
			return nil
		}
		return lex
	}
	fsys, err := presets.CrisisLexicon()
	if err != nil {
		l.fail("BIRGE_CRISIS_LEXICON_DIR: " + err.Error())
		return nil
	}
	lex, err := crisis.Load(fsys)
	if err != nil {
		l.fail("BIRGE_CRISIS_LEXICON_DIR: " + err.Error())
		return nil
	}
	cfg.DemoPresets = append(cfg.DemoPresets, presets.NameCrisisLexicon)
	return lex
}

func (l *loader) crisisPhonesOrDemo(cfg *Config) {
	if len(cfg.CrisisPhones) > 0 {
		return
	}
	// BIRGE_CRISIS_PHONES set to invalid JSON already failed in platform/config.
	// Empty / unset → DEMO phones from settings.json.
	s, err := l.demoSettings()
	if err != nil {
		l.fail("BIRGE_CRISIS_PHONES: " + err.Error())
		return
	}
	cfg.CrisisPhones = s.CrisisPhones
	cfg.DemoPresets = append(cfg.DemoPresets, presets.NameCrisisPhones)
}

func (l *loader) questionnaireOrDemo(cfg *Config) wellbeingservice.Settings {
	if cfg.QuestionnaireFile != "" {
		return l.questionnaire("BIRGE_QUESTIONNAIRE_FILE", cfg.QuestionnaireFile)
	}
	b, err := presets.Questionnaire()
	if err != nil {
		l.fail("BIRGE_QUESTIONNAIRE_FILE: " + err.Error())
		return wellbeingservice.Settings{}
	}
	settings := l.questionnaireBytes("presets/"+presets.NameQuestionnaire, b)
	if settings.Questionnaire.Version != "" {
		cfg.DemoPresets = append(cfg.DemoPresets, presets.NameQuestionnaire)
	}
	return settings
}

// demoSettings caches the embedded settings.json for consent versions and phones.
func (l *loader) demoSettings() (presets.Settings, error) {
	if l.settingsOK {
		return l.settings, nil
	}
	s, err := presets.LoadSettings()
	if err != nil {
		return presets.Settings{}, err
	}
	l.settings = s
	l.settingsOK = true
	return s, nil
}

func (l *loader) lang(name, def string) string {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	if v != "kk" && v != "ru" {
		l.fail(name + `: must be "kk" or "ru"`)
		return def
	}
	return v
}

func (l *loader) timeZone(name, def string) string {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		v = def
	}
	if _, err := time.LoadLocation(v); err != nil {
		l.fail(name + ": must be a valid IANA time zone (host needs current tzdata)")
		return def
	}
	return v
}

// questionnaireFile is the on-disk shape of BIRGE_QUESTIONNAIRE_FILE.
// Thresholds are set by the psychologist; the example file ships placeholders.
type questionnaireFile struct {
	Version                 string `json:"version"`
	Questions               []questionnaireQuestion
	ReverseScoreQuestionIDs []string                          `json:"reverse_score_question_ids"`
	CrisisItemQuestionID    string                            `json:"crisis_item_question_id"`
	Thresholds              map[string]questionnaireThreshold `json:"thresholds"`
	FeedbackByFlag          map[string]questionnaireFeedback  `json:"feedback_by_flag"`
}

type questionnaireQuestion struct {
	ID         string                `json:"id"`
	Instrument string                `json:"instrument"`
	TextKey    string                `json:"text_key"`
	Options    []questionnaireOption `json:"options"`
}

type questionnaireOption struct {
	Value   int    `json:"value"`
	TextKey string `json:"text_key"`
}

type questionnaireThreshold struct {
	ElevatedAt int `json:"elevated_at"`
	CrisisAt   int `json:"crisis_at"`
}

type questionnaireFeedback struct {
	FeedbackKey string `json:"feedback_key"`
	Suggestion  string `json:"suggestion"`
}

func (l *loader) questionnaire(name, path string) wellbeingservice.Settings {
	if path == "" {
		return wellbeingservice.Settings{}
	}
	b, err := l.readFile(path)
	if err != nil {
		l.fail(name + ": cannot read the questionnaire file")
		return wellbeingservice.Settings{}
	}
	return l.questionnaireBytes(name, b)
}

func (l *loader) questionnaireBytes(name string, b []byte) wellbeingservice.Settings {
	var raw questionnaireFile
	if err := json.Unmarshal(b, &raw); err != nil {
		l.fail(name + ": must be a JSON questionnaire object")
		return wellbeingservice.Settings{}
	}
	if raw.Version == "" || len(raw.Questions) == 0 {
		l.fail(name + ": version and questions are required")
		return wellbeingservice.Settings{}
	}
	questions := make([]wellbeingmodel.Question, 0, len(raw.Questions))
	for _, q := range raw.Questions {
		if q.ID == "" || !wellbeingmodel.InstrumentIsValid(q.Instrument) || q.TextKey == "" || len(q.Options) == 0 {
			l.fail(name + ": each question needs id, instrument (phq4|pss4), text_key and options")
			return wellbeingservice.Settings{}
		}
		opts := make([]wellbeingmodel.QuestionOption, 0, len(q.Options))
		for _, o := range q.Options {
			if o.TextKey == "" {
				l.fail(name + ": each option needs text_key")
				return wellbeingservice.Settings{}
			}
			opts = append(opts, wellbeingmodel.QuestionOption{Value: o.Value, TextKey: o.TextKey})
		}
		questions = append(questions, wellbeingmodel.Question{
			ID: q.ID, Instrument: q.Instrument, TextKey: q.TextKey, Options: opts,
		})
	}
	if len(raw.Thresholds) == 0 || len(raw.FeedbackByFlag) == 0 {
		l.fail(name + ": thresholds and feedback_by_flag are required")
		return wellbeingservice.Settings{}
	}
	thresholds := make(map[string]wellbeingmodel.InstrumentThresholds, len(raw.Thresholds))
	for inst, th := range raw.Thresholds {
		if !wellbeingmodel.InstrumentIsValid(inst) {
			l.fail(name + ": unknown instrument in thresholds")
			return wellbeingservice.Settings{}
		}
		thresholds[inst] = wellbeingmodel.InstrumentThresholds{ElevatedAt: th.ElevatedAt, CrisisAt: th.CrisisAt}
	}
	feedback := make(map[string]wellbeingmodel.Feedback, len(raw.FeedbackByFlag))
	for flag, fb := range raw.FeedbackByFlag {
		if !wellbeingmodel.FlagIsValid(flag) || fb.FeedbackKey == "" || !wellbeingmodel.SuggestionIsValid(fb.Suggestion) {
			l.fail(name + ": feedback_by_flag needs a known flag, feedback_key and suggestion")
			return wellbeingservice.Settings{}
		}
		feedback[flag] = wellbeingmodel.Feedback{FeedbackKey: fb.FeedbackKey, Suggestion: fb.Suggestion}
	}
	for _, flag := range []string{wellbeingmodel.FlagNone, wellbeingmodel.FlagElevated, wellbeingmodel.FlagCrisis} {
		if _, ok := feedback[flag]; !ok {
			l.fail(name + ": feedback_by_flag missing flag " + flag)
			return wellbeingservice.Settings{}
		}
	}
	return wellbeingservice.Settings{
		Questionnaire:           wellbeingmodel.Questionnaire{Version: raw.Version, Questions: questions},
		ReverseScoreQuestionIDs: append([]string(nil), raw.ReverseScoreQuestionIDs...),
		CrisisItemQuestionID:    raw.CrisisItemQuestionID,
		Thresholds:              thresholds,
		FeedbackByFlag:          feedback,
	}
}

func wellbeingCrisisPhones(phones []config.CrisisPhone) []wellbeingmodel.CrisisPhone {
	out := make([]wellbeingmodel.CrisisPhone, 0, len(phones))
	for _, p := range phones {
		out = append(out, wellbeingmodel.CrisisPhone{Number: p.Number, Label: p.Label})
	}
	return out
}

func safetyCrisisPhones(phones []config.CrisisPhone) []safetymodel.CrisisPhone {
	out := make([]safetymodel.CrisisPhone, 0, len(phones))
	for _, p := range phones {
		out = append(out, safetymodel.CrisisPhone{Number: p.Number, Label: p.Label})
	}
	return out
}
