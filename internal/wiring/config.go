package wiring

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	analyticsmodel "birge/internal/domains/analytics/model"
	"birge/internal/domains/assistant/anonymize"
	assistantservice "birge/internal/domains/assistant/service"
	"birge/internal/domains/knowledge/retrieval"
	pushadapter "birge/internal/domains/notify/adapter/push"
	smsadapter "birge/internal/domains/notify/adapter/smsgw"
	"birge/internal/domains/notify/sms/guard"
	privacymodel "birge/internal/domains/privacy/model"
	"birge/internal/domains/safety/crisis"
	safetymodel "birge/internal/domains/safety/model"
	wellbeingmodel "birge/internal/domains/wellbeing/model"
	wellbeingservice "birge/internal/domains/wellbeing/service"
	"birge/internal/platform/config"
	"birge/internal/platform/crypto"
	"birge/internal/platform/objectstore"
	"birge/internal/platform/ratelimit"
	"birge/internal/platform/staffauth"
	"birge/presets"
)

// Command is a subcommand of the birge binary; it decides which settings are required.
type Command string

// Subcommands.
const (
	CommandAPI       Command = "api"
	CommandWorker    Command = "worker"
	CommandMigrate   Command = "migrate"
	CommandKBReindex Command = "kb-reindex"
	CommandKBExport  Command = "kb-export"
)

// Assistant / retrieval defaults (docs/development.md § Configuration).
const (
	defaultLLMModel            = "claude-opus-5-5"
	defaultAssistantDailyLimit = 50
	defaultQdrantAlias         = "birge-kb"
	defaultTEIQueryPrefix      = "query: "
	defaultTEIPassagePrefix    = "passage: "
	defaultQdrantScoreThresh   = float32(0.3)
)

// Delivery modes (BIRGE_DELIVERY_MODE). Only DeliveryModeDev may use the
// log-only senders; production must name real providers.
const (
	DeliveryModeDev        = "dev"
	DeliveryModeProduction = "production"
)

// Rate limit stores (BIRGE_RATE_LIMIT_STORE).
const (
	// RateLimitStorePostgres shares the rate limits, the PoW replay claims
	// and the SMS guard counters across every API and worker replica
	// (platform.rate_counters, platform.one_time_claims). The default.
	RateLimitStorePostgres = "postgres"
	// RateLimitStoreMemory keeps them in each process: tests and a single
	// replica only, since with N replicas every limit is N times higher.
	RateLimitStoreMemory = "memory"
)

// Push and SMS provider names (BIRGE_PUSH_PROVIDER, BIRGE_SMS_PROVIDER).
const (
	ProviderLog  = "log"
	ProviderFCM  = "fcm"
	ProviderAPNs = "apns"
	ProviderSMSC = "smsc"
)

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
	// SOS auto-close: requirements § 4.6; allow shorter values in tests.
	defaultSosAutoClose = 24 * time.Hour
	maxSosAutoClose     = 168 * time.Hour // 7 days
	defaultStaffSMSLang = "ru"
	defaultTimeZone     = "Asia/Almaty"
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
	// PreviousKEKs are retired KEKs (BIRGE_KEK_PREVIOUS_KEYSET_FILES): they
	// only unwrap member keys wrapped before a rotation, which are then
	// re-wrapped with KEK on first use. Never used for new keys.
	PreviousKEKs []*crypto.LocalKEK
	// RateLimitStore is RateLimitStorePostgres or RateLimitStoreMemory
	// (BIRGE_RATE_LIMIT_STORE).
	RateLimitStore string
	// BlindIndexKey is reserved for blind indexes (crypto.BlindIndexer); no
	// context uses one yet, so it is optional.
	BlindIndexKey []byte
	PoWKey        []byte
	HashKey       []byte
	PoWMaxNumber  int64
	// PlanLinkKey is the HMAC key of external plan link tokens
	// (BIRGE_PLAN_LINK_KEY_FILE): the API looks links up with it, the worker
	// mints them when it sends the plan SMS.
	PlanLinkKey []byte
	// AnalyticsKey is the pseudonym key of analytics facts
	// (BIRGE_ANALYTICS_KEY_FILE, worker): HMAC-SHA256(key, member_id). Its
	// own key, never another one: whoever holds it can test whether a known
	// member ID has facts (ADR-0024).
	AnalyticsKey []byte
	// AnalyticsK is the small-cell threshold of the metrics (BIRGE_ANALYTICS_K,
	// API): default 5; 1 on the DEMO settings preset (ADR-0024).
	AnalyticsK int
	// PublicURL is the public origin of the member web app
	// (BIRGE_PUBLIC_URL, worker): plan SMS links are PublicURL + "/plan#" + token.
	PublicURL string

	// InternalAddr is the worker's internal listener (BIRGE_INTERNAL_ADDR),
	// serving the Kratos courier hook; empty means no internal listener and
	// no hook. Never route it from the ingress.
	InternalAddr string
	// CourierSecret is the shared secret Kratos' courier presents to the
	// hook (BIRGE_COURIER_SECRET_FILE, at least 32 bytes); required when
	// InternalAddr is set.
	CourierSecret []byte

	// SMSPrefixes are the allowed E.164 mobile prefixes, shared by the auth
	// gateway and the notify SMS guard.
	SMSPrefixes []string
	SMSGuard    guard.Config
	// SMSTexts maps template -> language -> neutral fixed SMS body (ADR-0013).
	SMSTexts map[string]map[string]string

	AuthPerIP, AuthPerNumber, AuthGlobal ratelimit.Rate
	APIPerIP                             ratelimit.Rate
	// PlanLinkPerIP limits viewExternalPlan and, in a bucket of its own,
	// viewExternalSos per client IP (BIRGE_PLAN_LINK_RATE_PER_IP).
	PlanLinkPerIP ratelimit.Rate

	// TrustedProxyHeader, when set, names the header whose right-most value
	// is the client IP (set by the ingress); otherwise RemoteAddr is used.
	TrustedProxyHeader string

	ConsentVersions  map[string]string
	DeletionDelay    time.Duration
	CriticalSMSAfter time.Duration
	DeliveryMode     string
	ApplyRoles       bool
	// ReferencePresets names the reference sets (presets/<name>/reference.json)
	// migrate syncs into the membership reference tables
	// (BIRGE_REFERENCE_PRESETS; default presets.DefaultReferenceSets).
	ReferencePresets []string

	// Delivery providers (worker). PushProviders is [log] or a subset of
	// fcm and apns; SMSProvider is log or smsc. The credentials below are
	// parsed at startup so a bad file refuses to start instead of failing
	// every send later; they hold private keys and passwords.
	PushProviders []string
	SMSProvider   string
	FCM           pushadapter.FCMCredentials
	APNs          pushadapter.APNsCredentials
	APNsTopic     string
	APNsBaseURL   string
	SMSC          smsadapter.SMSC
	// PushTitles maps language -> neutral push title (BIRGE_PUSH_TITLES).
	PushTitles map[string]string

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

	// SosAutoCloseAfter is how long an open SOS stays open before the worker
	// closes it (BIRGE_SOS_AUTO_CLOSE_AFTER; default 24h).
	SosAutoCloseAfter time.Duration

	// Wellbeing: questionnaire from BIRGE_QUESTIONNAIRE_FILE or the EXAMPLE preset.
	Wellbeing         wellbeingservice.Settings
	QuestionnaireFile string
	TimeZone          string
	PracticesBaseURL  string

	// DemoPresets lists embedded presets in use (logged once at Warn on start).
	DemoPresets []string
	// StaffAuth configures staff token introspection (ADR-0008); it holds
	// the client secret. Nil when no BIRGE_STAFF_* provider variable is set:
	// staff routes then answer 503 and never fall back to member sessions.
	// Clock, Logger and Client are filled in by NewAPIHandler.
	StaffAuth *staffauth.Config

	// Assistant (Talk / Ask in my own words). LLMAPIKey empty → degraded mode
	// (Warn LLMDisabled); never refuses to start.
	LLMModel            string
	LLMAPIKey           string
	LLMBaseURL          string
	AssistantDailyLimit int
	AssistantPrompt     assistantservice.Prompt
	AssistantNames      []string

	// Retrieval (ADR-0010). When TEIURL, QdrantURL or EmbeddingModelID is
	// unset, RetrievalEnabled is false and the API uses NoRetrieval
	// (Warn RetrievalDisabled).
	TEIURL               string
	TEIQueryPrefix       string
	TEIPassagePrefix     string
	EmbeddingModelID     string
	QdrantURL            string
	QdrantAlias          string
	QdrantScoreThreshold float32
	RetrievalEnabled     bool

	// ObjectStore is S3-compatible storage for item photos (ADR-0015). Nil
	// config fields mean Disabled: photo uploads answer 503.
	ObjectStore objectstore.Config
	// ObjectStoreEnabled is true when BIRGE_S3_ENDPOINT is set.
	ObjectStoreEnabled bool

	// ChatWSOrigins are the WebSocket OriginPatterns for GET /v1/chat/ws
	// (comma-separated; empty allows non-browser clients with no Origin).
	ChatWSOrigins []string
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
		cfg.ReferencePresets = l.referencePresets("BIRGE_REFERENCE_PRESETS")
		return cfg, l.result()
	}
	if cmd == CommandKBReindex {
		l.retrieval(&cfg, true)
		return cfg, l.result()
	}
	if cmd == CommandKBExport {
		// Only DATABASE_URL is required (already loaded above).
		return cfg, l.result()
	}
	if !api && !worker {
		return Config{}, fmt.Errorf("configuration: unknown command %q", cmd)
	}

	cfg.KEK = l.kek("BIRGE_KEK_ID", "BIRGE_KEK_KEYSET_FILE")
	cfg.PreviousKEKs = l.previousKEKs("BIRGE_KEK_PREVIOUS_KEYSET_FILES", cfg.KEK)
	cfg.RateLimitStore = l.rateLimitStore("BIRGE_RATE_LIMIT_STORE")
	cfg.BlindIndexKey = l.keyFile("BIRGE_BLIND_INDEX_KEY_FILE", false)
	cfg.SMSPrefixes = l.prefixes("BIRGE_SMS_ALLOWED_PREFIXES")
	cfg.DeletionDelay = l.duration("BIRGE_DELETION_DELAY", 24*time.Hour, maxDeletionDelay)
	cfg.CriticalSMSAfter = l.duration("NOTIFY_CRITICAL_SMS_AFTER", 60*time.Second, maxCriticalSMSAfter)
	cfg.TrustedProxyHeader = l.headerName("BIRGE_TRUSTED_PROXY_HEADER")
	cfg.CrisisLexiconDir = strings.TrimSpace(getenv("BIRGE_CRISIS_LEXICON_DIR"))
	cfg.Lexicon = l.lexiconOrDemo(&cfg)
	cfg.EscalationBackupAfter = l.duration("BIRGE_ESCALATION_BACKUP_AFTER", defaultEscalationBackup, maxEscalationBackup)
	cfg.SosAutoCloseAfter = l.duration("BIRGE_SOS_AUTO_CLOSE_AFTER", defaultSosAutoClose, maxSosAutoClose)
	cfg.StaffSMSLang = l.lang("BIRGE_STAFF_SMS_LANG", defaultStaffSMSLang)
	cfg.TimeZone = l.timeZone("BIRGE_TIME_ZONE", defaultTimeZone)
	// Kratos admin is needed on the API for staff contact reveal (MemberDirectory)
	// and on the worker for member SMS PhoneFor.
	cfg.KratosAdminURL = l.httpURL("KRATOS_ADMIN_URL", true)
	cfg.PlanLinkKey = l.keyFile("BIRGE_PLAN_LINK_KEY_FILE", true)
	l.crisisPhonesOrDemo(&cfg)

	if api {
		cfg.KratosPublicURL = l.httpURL("KRATOS_PUBLIC_URL", true)
		cfg.NATSURL = l.natsURL("NATS_URL") // chat WebSocket fan-out (NATS core)
		cfg.PoWKey = l.keyFile("BIRGE_POW_KEY_FILE", true)
		cfg.HashKey = l.keyFile("BIRGE_HASH_KEY_FILE", true)
		if cfg.PoWKey != nil && bytes.Equal(cfg.PoWKey, cfg.HashKey) {
			l.fail("BIRGE_POW_KEY_FILE and BIRGE_HASH_KEY_FILE must hold different keys")
		}
		if cfg.PlanLinkKey != nil && (bytes.Equal(cfg.PlanLinkKey, cfg.HashKey) || bytes.Equal(cfg.PlanLinkKey, cfg.PoWKey)) {
			l.fail("BIRGE_PLAN_LINK_KEY_FILE must hold its own key, not the PoW or hash key")
		}
		cfg.PoWMaxNumber = int64(l.positiveInt("BIRGE_AUTH_POW_MAX_NUMBER", 100_000))
		cfg.AuthPerIP = l.rate("BIRGE_AUTH_RATE_PER_IP", "20/1m")
		cfg.AuthPerNumber = l.rate("BIRGE_AUTH_RATE_PER_NUMBER", "3/10m")
		cfg.AuthGlobal = l.rate("BIRGE_AUTH_RATE_GLOBAL", "20/1s")
		cfg.APIPerIP = l.rate("BIRGE_API_RATE_PER_IP", "300/100ms")
		cfg.PlanLinkPerIP = l.rate("BIRGE_PLAN_LINK_RATE_PER_IP", "10/1m")
		cfg.ConsentVersions = l.consentVersionsOrDemo(&cfg)
		cfg.AnalyticsK = l.analyticsK(&cfg)
		cfg.QuestionnaireFile = strings.TrimSpace(getenv("BIRGE_QUESTIONNAIRE_FILE"))
		cfg.Wellbeing = l.questionnaireOrDemo(&cfg)
		cfg.PracticesBaseURL = l.httpURL("BIRGE_PRACTICES_BASE_URL", true)
		cfg.Wellbeing.PracticesBaseURL = cfg.PracticesBaseURL
		cfg.Wellbeing.TimeZone = cfg.TimeZone
		cfg.Wellbeing.CrisisPhones = wellbeingCrisisPhones(cfg.CrisisPhones)
		cfg.StaffAuth = l.staffAuth()
		cfg.ChatWSOrigins = l.list("BIRGE_CHAT_WS_ORIGINS")
		l.assistant(&cfg)
		l.retrieval(&cfg, false)
		l.objectStore(&cfg)
	}
	if worker {
		cfg.NATSURL = l.natsURL("NATS_URL")
		cfg.PublicURL = l.httpURL("BIRGE_PUBLIC_URL", true)
		cfg.AnalyticsKey = l.keyFile("BIRGE_ANALYTICS_KEY_FILE", true)
		if cfg.AnalyticsKey != nil && bytes.Equal(cfg.AnalyticsKey, cfg.PlanLinkKey) {
			l.fail("BIRGE_ANALYTICS_KEY_FILE must hold its own key, not the plan link key")
		}
		// The shared SMS guard counters store numbers only as a keyed hash,
		// with the same key as the API's limiter tables.
		cfg.HashKey = l.keyFile("BIRGE_HASH_KEY_FILE", cfg.RateLimitStore == RateLimitStorePostgres)
		if cfg.HashKey != nil && (bytes.Equal(cfg.HashKey, cfg.PlanLinkKey) || bytes.Equal(cfg.HashKey, cfg.AnalyticsKey)) {
			l.fail("BIRGE_HASH_KEY_FILE must hold its own key, not the plan link or analytics key")
		}
		l.objectStore(&cfg)
		cfg.SMSGuard = guard.Config{
			AllowedPrefixes: cfg.SMSPrefixes,
			PerNumberLimit:  l.positiveInt("BIRGE_SMS_PER_NUMBER_LIMIT", 5),
			PerNumberWindow: l.duration("BIRGE_SMS_PER_NUMBER_WINDOW", time.Hour, 24*time.Hour),
			GlobalLimit:     l.positiveInt("BIRGE_SMS_GLOBAL_LIMIT", 60),
			GlobalWindow:    l.duration("BIRGE_SMS_GLOBAL_WINDOW", time.Minute, 24*time.Hour),
			DailyBudget:     l.positiveInt("BIRGE_SMS_DAILY_BUDGET", 1000),
		}
		cfg.SMSTexts = l.smsTextsOrDemo(&cfg)
		l.internalListener(&cfg)
		cfg.EventsMaxAge = l.duration("BIRGE_EVENTS_MAX_AGE", defaultEventsMaxAge, maxEventsMaxAge)
		cfg.EventsMaxBytes = l.positiveInt64("BIRGE_EVENTS_MAX_BYTES", defaultEventsMaxBytes)
		// Reminder scheduling needs the deployment zone; questionnaire is API-only.
		cfg.Wellbeing.TimeZone = cfg.TimeZone
		cfg.Wellbeing.CrisisPhones = wellbeingCrisisPhones(cfg.CrisisPhones)
		l.delivery(&cfg)
		// Outside development, SMS bodies must be configured: an empty map
		// would skip every SMS, including critical ones. Unset falls back to
		// the DEMO preset above; an explicit empty object still fails here.
		if cfg.DeliveryMode != DeliveryModeDev && len(cfg.SMSTexts) == 0 {
			l.fail("BIRGE_SMS_TEXTS is required when BIRGE_DELIVERY_MODE is not dev")
		}
		// Worker runs the retrieval indexer when TEI+Qdrant are configured.
		l.retrieval(&cfg, false)
	}
	if err := l.result(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// internalListener loads the worker's internal listener and the courier
// hook secret. The listener is opt-in: a worker without it serves no hook,
// and Kratos keeps its own courier configuration. It must not share the
// health port, which an orchestrator may expose more widely.
func (l *loader) internalListener(cfg *Config) {
	cfg.InternalAddr = strings.TrimSpace(l.getenv("BIRGE_INTERNAL_ADDR"))
	if cfg.InternalAddr == "" {
		return
	}
	_, port, err := net.SplitHostPort(cfg.InternalAddr)
	if err != nil || port == "" {
		l.fail("BIRGE_INTERNAL_ADDR: must be host:port or :port")
		return
	}
	if _, httpPort, err := net.SplitHostPort(cfg.HTTPAddr); err == nil && httpPort == port {
		l.fail("BIRGE_INTERNAL_ADDR: must use another port than BIRGE_HTTP_ADDR")
	}
	cfg.CourierSecret = l.keyFile("BIRGE_COURIER_SECRET_FILE", true)
}

// delivery loads the delivery mode, the push and SMS providers and their
// credentials. The log-only senders drop every notification, so outside
// development they refuse to start, the same way as missing SMS texts.
func (l *loader) delivery(cfg *Config) {
	switch cfg.DeliveryMode = strings.TrimSpace(l.getenv("BIRGE_DELIVERY_MODE")); cfg.DeliveryMode {
	case DeliveryModeDev, DeliveryModeProduction:
	case "":
		l.fail(`BIRGE_DELIVERY_MODE is required: "production" with real push and SMS providers, or "dev" for the log-only senders`)
	default:
		l.fail(`BIRGE_DELIVERY_MODE: must be "dev" or "production"`)
	}
	dev := cfg.DeliveryMode == DeliveryModeDev

	cfg.PushProviders = l.list("BIRGE_PUSH_PROVIDER")
	if len(cfg.PushProviders) == 0 && dev {
		cfg.PushProviders = []string{ProviderLog}
	}
	seen := map[string]bool{}
	for _, p := range cfg.PushProviders {
		switch {
		case p == ProviderLog && len(cfg.PushProviders) == 1:
		case p == ProviderFCM || p == ProviderAPNs:
		default:
			l.fail(`BIRGE_PUSH_PROVIDER: must be "log" or a comma list of "fcm" and "apns"`)
		}
		if seen[p] {
			l.fail("BIRGE_PUSH_PROVIDER: " + p + " is listed twice")
		}
		seen[p] = true
	}
	switch {
	case len(cfg.PushProviders) == 0:
		l.fail(`BIRGE_PUSH_PROVIDER is required when BIRGE_DELIVERY_MODE is not dev ("fcm", "apns" or "fcm,apns")`)
	case seen[ProviderLog] && !dev:
		l.fail(`BIRGE_PUSH_PROVIDER: "log" drops every push and is refused when BIRGE_DELIVERY_MODE is not dev`)
	}
	if seen[ProviderFCM] {
		if b := l.secretBytes("BIRGE_FCM_SERVICE_ACCOUNT_FILE"); b != nil {
			creds, err := pushadapter.ParseFCMServiceAccount(b)
			clear(b)
			if err != nil {
				l.fail("BIRGE_FCM_SERVICE_ACCOUNT_FILE: " + err.Error())
			}
			cfg.FCM = creds
		}
	}
	if seen[ProviderAPNs] {
		keyID := l.required("BIRGE_APNS_KEY_ID")
		teamID := l.required("BIRGE_APNS_TEAM_ID")
		cfg.APNsTopic = l.required("BIRGE_APNS_TOPIC")
		if b := l.secretBytes("BIRGE_APNS_KEY_FILE"); b != nil && keyID != "" && teamID != "" {
			creds, err := pushadapter.ParseAPNsKey(b, keyID, teamID)
			clear(b)
			if err != nil {
				l.fail("BIRGE_APNS_KEY_FILE: " + err.Error())
			}
			cfg.APNs = creds
		}
		switch env := strings.TrimSpace(l.getenv("BIRGE_APNS_ENVIRONMENT")); env {
		case "", "production":
			cfg.APNsBaseURL = pushadapter.DefaultAPNsBaseURL
		case "sandbox":
			cfg.APNsBaseURL = pushadapter.APNsSandboxBaseURL
		default:
			l.fail(`BIRGE_APNS_ENVIRONMENT: must be "production" or "sandbox"`)
		}
	}
	cfg.PushTitles = l.pushTitles("BIRGE_PUSH_TITLES")

	cfg.SMSProvider = strings.TrimSpace(l.getenv("BIRGE_SMS_PROVIDER"))
	if cfg.SMSProvider == "" && dev {
		cfg.SMSProvider = ProviderLog
	}
	switch cfg.SMSProvider {
	case ProviderLog:
		if !dev {
			l.fail(`BIRGE_SMS_PROVIDER: "log" drops every SMS, including critical ones, and is refused when BIRGE_DELIVERY_MODE is not dev`)
		}
	case ProviderSMSC:
		cfg.SMSC = smsadapter.SMSC{
			Login:    l.secretFile("BIRGE_SMSC_LOGIN_FILE"),
			Password: l.secretFile("BIRGE_SMSC_PASSWORD_FILE"),
			Sender:   l.smsSender("BIRGE_SMSC_SENDER"),
		}
	case "":
		l.fail(`BIRGE_SMS_PROVIDER is required when BIRGE_DELIVERY_MODE is not dev ("smsc")`)
	default:
		l.fail(`BIRGE_SMS_PROVIDER: must be "log" or "smsc"`)
	}
}

// secretBytes reads a credential file named by name. The caller clears the
// bytes after parsing them.
func (l *loader) secretBytes(name string) []byte {
	path := strings.TrimSpace(l.getenv(name))
	if path == "" {
		l.fail(name + " is required (path to the credential file)")
		return nil
	}
	b, err := l.readFile(path)
	if err != nil {
		l.fail(name + ": cannot read the credential file")
		return nil
	}
	return b
}

// pushTitles parses {"<lang>": "<title>"}; unset means the product name.
func (l *loader) pushTitles(name string) map[string]string {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		l.fail(name + `: must be a JSON object {"<lang>": "<title>"}`)
		return nil
	}
	for lang, title := range out {
		if lang != "kk" && lang != "ru" && lang != "en" {
			l.fail(name + `: languages are "kk", "ru" and "en"`)
		}
		if strings.TrimSpace(title) == "" || len(title) > 100 {
			l.fail(name + ": every title must be 1 to 100 bytes")
		}
	}
	return out
}

var smsSenderRe = regexp.MustCompile(`^[A-Za-z0-9 ]{1,11}$`)

// smsSender reads an optional sender ID: up to 11 Latin letters, digits or
// spaces, which is what SMS gateways accept as an alphanumeric sender.
func (l *loader) smsSender(name string) string {
	v := strings.TrimSpace(l.getenv(name))
	if v != "" && !smsSenderRe.MatchString(v) {
		l.fail(name + ": up to 11 Latin letters, digits or spaces")
		return ""
	}
	return v
}

// assistant loads the LLM, prompt, names dictionary and daily limit.
// A missing API key is allowed: the assistant runs degraded (LLMDisabled).
func (l *loader) assistant(cfg *Config) {
	cfg.LLMModel = strings.TrimSpace(l.getenv("BIRGE_LLM_MODEL"))
	if cfg.LLMModel == "" {
		cfg.LLMModel = defaultLLMModel
	}
	cfg.LLMBaseURL = l.httpURL("BIRGE_LLM_BASE_URL", false)
	cfg.LLMAPIKey = l.secretFileOptional("BIRGE_LLM_API_KEY_FILE")
	cfg.AssistantDailyLimit = l.positiveInt("BIRGE_ASSISTANT_DAILY_LIMIT", defaultAssistantDailyLimit)
	cfg.AssistantPrompt = l.assistantPromptOrDemo(cfg)
	cfg.AssistantNames = l.assistantNames()
}

func (l *loader) assistantPromptOrDemo(cfg *Config) assistantservice.Prompt {
	path := strings.TrimSpace(l.getenv("BIRGE_ASSISTANT_PROMPT_FILE"))
	if path != "" {
		b, err := l.readFile(path)
		if err != nil {
			l.fail("BIRGE_ASSISTANT_PROMPT_FILE: cannot read the prompt file")
			return assistantservice.Prompt{}
		}
		text := strings.TrimSpace(string(b))
		if text == "" {
			l.fail("BIRGE_ASSISTANT_PROMPT_FILE: the prompt file is empty")
			return assistantservice.Prompt{}
		}
		base := path
		if i := strings.LastIndexAny(path, `/\`); i >= 0 {
			base = path[i+1:]
		}
		version := base
		if i := strings.LastIndex(base, "."); i > 0 {
			version = base[:i]
		}
		return assistantservice.Prompt{Version: version, Text: text}
	}
	version, text, err := presets.AssistantPrompt()
	if err != nil {
		l.fail("BIRGE_ASSISTANT_PROMPT_FILE: " + err.Error())
		return assistantservice.Prompt{}
	}
	cfg.DemoPresets = append(cfg.DemoPresets, presets.NameAssistantPrompt)
	return assistantservice.Prompt{Version: version, Text: strings.TrimSpace(string(text))}
}

func (l *loader) assistantNames() []string {
	path := strings.TrimSpace(l.getenv("BIRGE_ASSISTANT_NAMES_FILE"))
	if path == "" {
		return nil
	}
	b, err := l.readFile(path)
	if err != nil {
		l.fail("BIRGE_ASSISTANT_NAMES_FILE: cannot read the names file")
		return nil
	}
	names, err := anonymize.ParseNameList(strings.NewReader(string(b)))
	if err != nil {
		l.fail("BIRGE_ASSISTANT_NAMES_FILE: " + err.Error())
		return nil
	}
	return names
}

// retrieval loads TEI + Qdrant settings. required makes all of them mandatory
// (kb-reindex). Otherwise missing any one disables retrieval.
func (l *loader) retrieval(cfg *Config, required bool) {
	cfg.TEIURL = l.httpURL("BIRGE_TEI_URL", required)
	cfg.QdrantURL = l.httpURL("BIRGE_QDRANT_URL", required)
	cfg.EmbeddingModelID = strings.TrimSpace(l.getenv("BIRGE_EMBEDDING_MODEL_ID"))
	if required && cfg.EmbeddingModelID == "" {
		l.fail("BIRGE_EMBEDDING_MODEL_ID is required")
	}
	cfg.QdrantAlias = strings.TrimSpace(l.getenv("BIRGE_QDRANT_ALIAS"))
	if cfg.QdrantAlias == "" {
		cfg.QdrantAlias = defaultQdrantAlias
	}
	if v := l.getenv("BIRGE_TEI_QUERY_PREFIX"); v != "" {
		cfg.TEIQueryPrefix = v
	} else {
		cfg.TEIQueryPrefix = defaultTEIQueryPrefix
	}
	if v := l.getenv("BIRGE_TEI_PASSAGE_PREFIX"); v != "" {
		cfg.TEIPassagePrefix = v
	} else {
		cfg.TEIPassagePrefix = defaultTEIPassagePrefix
	}
	cfg.QdrantScoreThreshold = l.float32("BIRGE_QDRANT_SCORE_THRESHOLD", defaultQdrantScoreThresh)
	cfg.RetrievalEnabled = cfg.TEIURL != "" && cfg.QdrantURL != "" && cfg.EmbeddingModelID != ""
}

// QdrantCollection is the physical collection name for the configured model.
func (c Config) QdrantCollection() string {
	return retrieval.CollectionName(c.QdrantAlias, c.EmbeddingModelID)
}

// objectStore loads optional S3 settings. Unset BIRGE_S3_ENDPOINT → disabled
// (photo uploads 503). Any one of the other S3 variables set alone fails.
func (l *loader) objectStore(cfg *Config) {
	endpoint := strings.TrimSpace(l.getenv("BIRGE_S3_ENDPOINT"))
	bucket := strings.TrimSpace(l.getenv("BIRGE_S3_BUCKET"))
	region := strings.TrimSpace(l.getenv("BIRGE_S3_REGION"))
	public := strings.TrimSpace(l.getenv("BIRGE_S3_PUBLIC_BASE_URL"))
	accessPath := strings.TrimSpace(l.getenv("BIRGE_S3_ACCESS_KEY_FILE"))
	secretPath := strings.TrimSpace(l.getenv("BIRGE_S3_SECRET_KEY_FILE"))
	any := endpoint != "" || bucket != "" || region != "" || public != "" || accessPath != "" || secretPath != ""
	if !any {
		return
	}
	if endpoint == "" {
		l.fail("BIRGE_S3_ENDPOINT is required when any BIRGE_S3_* variable is set")
		return
	}
	cfg.ObjectStoreEnabled = true
	cfg.ObjectStore.Endpoint = endpoint
	cfg.ObjectStore.Bucket = l.required("BIRGE_S3_BUCKET")
	cfg.ObjectStore.Region = strings.TrimSpace(l.getenv("BIRGE_S3_REGION"))
	if cfg.ObjectStore.Region == "" {
		cfg.ObjectStore.Region = "garage"
	}
	cfg.ObjectStore.PublicBaseURL = l.httpURL("BIRGE_S3_PUBLIC_BASE_URL", true)
	cfg.ObjectStore.AccessKey = l.secretFile("BIRGE_S3_ACCESS_KEY_FILE")
	cfg.ObjectStore.SecretKey = l.secretFile("BIRGE_S3_SECRET_KEY_FILE")
}

// NewObjectStore builds the S3 store or Disabled when unset. Call once at
// start; log Warn ObjectStorageDisabled when disabled.
func NewObjectStore(cfg Config, logger *slog.Logger) (objectstore.Store, error) {
	if !cfg.ObjectStoreEnabled {
		logger.Warn("object storage disabled: photo uploads unavailable", "event", "ObjectStorageDisabled")
		return objectstore.Disabled{}, nil
	}
	store, err := objectstore.NewS3(cfg.ObjectStore)
	if err != nil {
		return nil, fmt.Errorf("wiring: object store: %w", err)
	}
	return store, nil
}

// secretFileOptional reads a secret from the file named by name; empty path
// returns "" without failing (assistant LLM key).
func (l *loader) secretFileOptional(name string) string {
	path := strings.TrimSpace(l.getenv(name))
	if path == "" {
		return ""
	}
	b, err := l.readFile(path)
	if err != nil {
		l.fail(name + ": cannot read the secret file")
		return ""
	}
	defer clear(b)
	s := string(bytes.TrimRight(b, " \t\r\n"))
	if s == "" {
		l.fail(name + ": the secret file is empty")
	}
	return s
}

func (l *loader) float32(name string, def float32) float32 {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return def
	}
	n, err := strconv.ParseFloat(v, 32)
	if err != nil || n < 0 || n > 1 {
		l.fail(name + ": must be a number between 0 and 1")
		return def
	}
	return float32(n)
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

// previousKEKs loads the retired KEKs from "<kek id>=<keyset file>" entries,
// comma-separated. Every listed file must load: a deployment that cannot
// open an old key would fail later on every member it wrapped, so it
// refuses to start instead, naming the KEK id (never the key).
func (l *loader) previousKEKs(name string, primary *crypto.LocalKEK) []*crypto.LocalKEK {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return nil
	}
	var out []*crypto.LocalKEK
	seen := map[string]bool{}
	for _, entry := range strings.Split(v, ",") {
		id, path, ok := strings.Cut(strings.TrimSpace(entry), "=")
		id, path = strings.TrimSpace(id), strings.TrimSpace(path)
		if !ok || id == "" || path == "" {
			l.fail(name + ": each entry must be <kek id>=<keyset file>, comma-separated")
			return nil
		}
		if primary != nil && id == primary.ID() {
			l.fail(name + ": KEK " + id + " is the primary (BIRGE_KEK_ID); list only retired KEKs")
			return nil
		}
		if seen[id] {
			l.fail(name + ": KEK " + id + " is listed twice")
			return nil
		}
		seen[id] = true
		b, err := l.readFile(path)
		if err != nil {
			l.fail(name + ": cannot read the keyset file of KEK " + id)
			return nil
		}
		k, err := crypto.NewLocalKEK(id, b)
		clear(b)
		if err != nil {
			// crypto errors never carry key material.
			l.fail(name + ": " + err.Error())
			return nil
		}
		out = append(out, k)
	}
	return out
}

// rateLimitStore reads BIRGE_RATE_LIMIT_STORE; unset is postgres, so a
// deployment that adds a replica keeps one set of limits without a change.
func (l *loader) rateLimitStore(name string) string {
	switch v := strings.TrimSpace(l.getenv(name)); v {
	case "", RateLimitStorePostgres:
		return RateLimitStorePostgres
	case RateLimitStoreMemory:
		return RateLimitStoreMemory
	default:
		l.fail(name + `: must be "postgres" or "memory"`)
		return RateLimitStorePostgres
	}
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

// rate parses "<burst>/<interval>": up to burst requests per fixed window of
// burst × interval (on average one per interval, see ratelimit.Rate).
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

// analyticsK is BIRGE_ANALYTICS_K when set. Unset, a deployment that runs on
// the DEMO settings preset (consent versions from presets) takes the preset's
// analytics_k (1, so demo data shows numbers) with the DemoPreset warning;
// any other deployment gets analyticsmodel.DefaultK (5).
func (l *loader) analyticsK(cfg *Config) int {
	if strings.TrimSpace(l.getenv("BIRGE_ANALYTICS_K")) != "" {
		return l.positiveInt("BIRGE_ANALYTICS_K", analyticsmodel.DefaultK)
	}
	if !slices.Contains(cfg.DemoPresets, presets.NameConsentVersions) {
		return analyticsmodel.DefaultK
	}
	s, err := l.demoSettings()
	if err != nil {
		l.fail("BIRGE_ANALYTICS_K: " + err.Error())
		return analyticsmodel.DefaultK
	}
	cfg.DemoPresets = append(cfg.DemoPresets, presets.NameAnalyticsK)
	return s.AnalyticsK
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

// staffAuthVars are the variables of the staff identity provider. Setting
// any of them turns staff authentication on and makes the required ones
// mandatory, so a half-configured provider refuses to start instead of
// silently leaving staff routes closed. (BIRGE_STAFF_SMS_LANG is unrelated.)
var staffAuthVars = []string{
	"BIRGE_STAFF_INTROSPECTION_URL", "BIRGE_STAFF_CLIENT_ID", "BIRGE_STAFF_CLIENT_SECRET_FILE",
	"BIRGE_STAFF_ISSUER", "BIRGE_STAFF_AUDIENCE", "BIRGE_STAFF_ROLES_CLAIM", "BIRGE_STAFF_ROLE_MAP",
	"BIRGE_STAFF_MFA_ACR", "BIRGE_STAFF_MFA_AMR",
}

// staffAuth reads the staff OIDC provider settings; nil when none is set.
func (l *loader) staffAuth() *staffauth.Config {
	set := false
	for _, name := range staffAuthVars {
		if strings.TrimSpace(l.getenv(name)) != "" {
			set = true
		}
	}
	if !set {
		return nil
	}
	errsBefore := len(l.errs)
	c := &staffauth.Config{
		IntrospectionURL: l.httpURL("BIRGE_STAFF_INTROSPECTION_URL", true),
		ClientID:         l.required("BIRGE_STAFF_CLIENT_ID"),
		ClientSecret:     l.secretFile("BIRGE_STAFF_CLIENT_SECRET_FILE"),
		Issuer:           l.issuer("BIRGE_STAFF_ISSUER"),
		Audience:         l.required("BIRGE_STAFF_AUDIENCE"),
		RolesClaim:       strings.TrimSpace(l.getenv("BIRGE_STAFF_ROLES_CLAIM")),
		RoleMap:          l.roleMap("BIRGE_STAFF_ROLE_MAP"),
		MFAACR:           l.list("BIRGE_STAFF_MFA_ACR"),
		MFAAMR:           l.list("BIRGE_STAFF_MFA_AMR"),
	}
	if len(c.MFAACR) == 0 && len(c.MFAAMR) == 0 {
		l.fail("BIRGE_STAFF_MFA_ACR or BIRGE_STAFF_MFA_AMR is required: MFA is mandatory for staff")
	}
	if len(l.errs) > errsBefore {
		return nil
	}
	// The middleware's own checks (claim path, MFA value syntax) run once
	// here so a bad value fails at startup. Its errors carry no values.
	if _, err := staffauth.New(*c); err != nil {
		l.fail("BIRGE_STAFF_*: " + err.Error())
		return nil
	}
	return c
}

// secretFile reads a secret issued by someone else (an IdP client secret)
// from the file named by name. Unlike keyFile there is no minimum length:
// the provider decides its format. Trailing whitespace is removed.
func (l *loader) secretFile(name string) string {
	path := strings.TrimSpace(l.getenv(name))
	if path == "" {
		l.fail(name + " is required (path to a file holding the secret)")
		return ""
	}
	b, err := l.readFile(path)
	if err != nil {
		l.fail(name + ": cannot read the secret file")
		return ""
	}
	defer clear(b)
	s := string(bytes.TrimRight(b, " \t\r\n"))
	if s == "" {
		l.fail(name + ": the secret file is empty")
	}
	return s
}

// issuer reads an OIDC issuer: an absolute http(s) URL kept verbatim,
// because tokens are compared with it exactly (a trailing slash matters).
func (l *loader) issuer(name string) string {
	v := l.required(name)
	if v == "" {
		return ""
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		l.fail(name + ": must be the provider's issuer, an absolute http(s) URL exactly as in its tokens")
		return ""
	}
	return v
}

// roleMap parses "<idp value>=<role>,..."; nil when unset (identity mapping).
func (l *loader) roleMap(name string) map[string]string {
	v := strings.TrimSpace(l.getenv(name))
	if v == "" {
		return nil
	}
	out := map[string]string{}
	for _, pair := range strings.Split(v, ",") {
		k, role, ok := strings.Cut(strings.TrimSpace(pair), "=")
		k, role = strings.TrimSpace(k), strings.TrimSpace(role)
		if !ok || k == "" || !staffauth.RoleIsValid(role) {
			l.fail(name + ": must be <idp value>=<role>, comma-separated, with roles " + strings.Join(staffauth.Roles, ", "))
			return nil
		}
		out[k] = role
	}
	return out
}

// list parses a comma-separated list, dropping empty items.
// referencePresets reads a comma-separated list of reference set names. Unset
// means presets.DefaultReferenceSets; an unknown or repeated name fails.
func (l *loader) referencePresets(name string) []string {
	names := l.list(name)
	if len(names) == 0 {
		return slices.Clone(presets.DefaultReferenceSets)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if !slices.Contains(presets.ReferenceSets, n) || seen[n] {
			l.fail(fmt.Sprintf("%s: unknown or repeated reference set (known: %s)", name, strings.Join(presets.ReferenceSets, ", ")))
			return nil
		}
		seen[n] = true
	}
	return names
}

func (l *loader) list(name string) []string {
	var out []string
	for _, s := range strings.Split(l.getenv(name), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
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
