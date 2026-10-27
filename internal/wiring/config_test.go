package wiring

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tink-crypto/tink-go/v2/aead"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
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

func validEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":               secretDatabaseURL,
		"NATS_URL":                   "nats://nats.example.invalid:4222",
		"KRATOS_PUBLIC_URL":          "http://kratos.example.invalid:4433/",
		"KRATOS_ADMIN_URL":           "http://kratos.example.invalid:4434",
		"BIRGE_KEK_ID":               "local-1",
		"BIRGE_KEK_KEYSET_FILE":      "/keys/kek.json",
		"BIRGE_POW_KEY_FILE":         "/keys/pow",
		"BIRGE_HASH_KEY_FILE":        "/keys/hash",
		"BIRGE_SMS_ALLOWED_PREFIXES": "+7700, +7701",
		"BIRGE_CONSENT_VERSIONS":     "data_processing=v1,ai_crossborder=v1,research=v2",
		"BIRGE_DELIVERY_MODE":        "dev",
	}
}

func validFiles(t *testing.T) fakeFiles {
	t.Helper()
	return fakeFiles{
		"/keys/kek.json": testKeysetJSON(t),
		"/keys/pow":      append(randomKey(t), '\n'),
		"/keys/hash":     randomKey(t),
	}
}

func load(env map[string]string, files fakeFiles, cmd Command) (Config, error) {
	return Load(func(k string) string { return env[k] }, files.read, cmd)
}

func TestLoadValid(t *testing.T) {
	files := validFiles(t)
	for _, cmd := range []Command{CommandAPI, CommandWorker, CommandMigrate} {
		t.Run(string(cmd), func(t *testing.T) {
			cfg, err := load(validEnv(), files, cmd)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.DatabaseURL != secretDatabaseURL {
				t.Error("DATABASE_URL not loaded")
			}
			if cmd == CommandMigrate {
				return
			}
			if cfg.KEK == nil || cfg.KEK.ID() != "local-1" {
				t.Error("KEK not loaded")
			}
			if cfg.DeletionDelay != 24*time.Hour || cfg.CriticalSMSAfter != 60*time.Second {
				t.Errorf("defaults: deletion %s, critical sms %s", cfg.DeletionDelay, cfg.CriticalSMSAfter)
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
				if cfg.ConsentVersions["research"] != "v2" || !cfg.APIPerIP.Valid() || cfg.AuthGlobal.Burst != 20 {
					t.Error("API settings not loaded")
				}
			}
			if cmd == CommandWorker && (cfg.NATSURL == "" || cfg.SMSGuard.DailyBudget != 1000) {
				t.Error("worker settings not loaded")
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
		{"missing kratos", CommandAPI, map[string]string{"KRATOS_PUBLIC_URL": ""}, nil, "KRATOS_PUBLIC_URL is required"},
		{"kratos with credentials", CommandAPI, map[string]string{"KRATOS_PUBLIC_URL": "http://u:p@kratos.example.invalid"}, nil, "KRATOS_PUBLIC_URL"},
		{"missing pow file", CommandAPI, nil, func(f fakeFiles) { delete(f, "/keys/pow") }, "BIRGE_POW_KEY_FILE: cannot read"},
		{"short key", CommandAPI, nil, func(f fakeFiles) { f["/keys/hash"] = []byte("short") }, "BIRGE_HASH_KEY_FILE: the key must be at least 32 bytes"},
		{"same keys", CommandAPI, nil, func(f fakeFiles) { f["/keys/hash"] = bytes.TrimSpace(f["/keys/pow"]) }, "must hold different keys"},
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
		{"no delivery mode", CommandWorker, map[string]string{"BIRGE_DELIVERY_MODE": ""}, nil, "no push or SMS provider is configured"},
		{"unknown delivery mode", CommandWorker, map[string]string{"BIRGE_DELIVERY_MODE": "production"}, nil, "only \"dev\" is supported"},
		{"bad sms texts", CommandWorker, map[string]string{"BIRGE_SMS_TEXTS": "[1]"}, nil, "BIRGE_SMS_TEXTS"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
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

func TestLoadNeverPrintsKeys(t *testing.T) {
	files := validFiles(t)
	files["/keys/hash"] = []byte("0123456789") // too short: rejected
	_, err := load(validEnv(), files, CommandAPI)
	if err == nil || strings.Contains(err.Error(), "0123456789") {
		t.Fatalf("error = %v: want a rejection without the key", err)
	}
	cfg, err := load(validEnv(), validFiles(t), CommandAPI)
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
	if _, err := load(validEnv(), validFiles(t), "serve"); err == nil {
		t.Fatal("unknown command accepted")
	}
}
