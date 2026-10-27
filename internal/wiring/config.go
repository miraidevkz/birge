package wiring

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"birge/internal/domains/membership/authgw"
	"birge/internal/domains/notify/sms/guard"
	privacymodel "birge/internal/domains/privacy/model"
	"birge/internal/platform/config"
	"birge/internal/platform/crypto"
	"birge/internal/platform/ratelimit"
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
		cfg.ConsentVersions = l.consentVersions("BIRGE_CONSENT_VERSIONS")
	}
	if worker {
		cfg.NATSURL = l.natsURL("NATS_URL")
		cfg.KratosAdminURL = l.httpURL("KRATOS_ADMIN_URL", true)
		cfg.SMSGuard = guard.Config{
			AllowedPrefixes: cfg.SMSPrefixes,
			PerNumberLimit:  l.positiveInt("BIRGE_SMS_PER_NUMBER_LIMIT", 5),
			PerNumberWindow: l.duration("BIRGE_SMS_PER_NUMBER_WINDOW", time.Hour, 24*time.Hour),
			GlobalLimit:     l.positiveInt("BIRGE_SMS_GLOBAL_LIMIT", 60),
			GlobalWindow:    l.duration("BIRGE_SMS_GLOBAL_WINDOW", time.Minute, 24*time.Hour),
			DailyBudget:     l.positiveInt("BIRGE_SMS_DAILY_BUDGET", 1000),
		}
		cfg.SMSTexts = l.smsTexts("BIRGE_SMS_TEXTS")
		cfg.EventsMaxAge = l.duration("BIRGE_EVENTS_MAX_AGE", defaultEventsMaxAge, maxEventsMaxAge)
		cfg.EventsMaxBytes = l.positiveInt64("BIRGE_EVENTS_MAX_BYTES", defaultEventsMaxBytes)
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
		// would skip every SMS, including critical ones.
		if cfg.DeliveryMode != DeliveryModeDev && len(cfg.SMSTexts) == 0 {
			l.fail("BIRGE_SMS_TEXTS is required when BIRGE_DELIVERY_MODE is not dev")
		}
	}
	if err := l.result(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// loader collects problems so that Load can report all of them at once.
type loader struct {
	getenv   func(string) string
	readFile func(string) ([]byte, error)
	errs     []error
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
func (l *loader) consentVersions(name string) map[string]string {
	v := l.required(name)
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

// smsTexts parses the JSON object {"<template>": {"<lang>": "<text>"}}.
func (l *loader) smsTexts(name string) map[string]map[string]string {
	v := strings.TrimSpace(l.getenv(name))
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
