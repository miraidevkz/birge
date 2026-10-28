package wiring

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"strings"
	"testing"

	pushadapter "birge/internal/domains/notify/adapter/push"
	smsadapter "birge/internal/domains/notify/adapter/smsgw"
)

const (
	testSMSCLogin    = "TEST-smsc-login"
	testSMSCPassword = "TEST-smsc-password-s3cret"
)

// providerFiles adds freshly generated provider credentials to files; no
// real credential is ever stored in the repository.
func providerFiles(t *testing.T, files fakeFiles) {
	t.Helper()
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(rk)
	if err != nil {
		t.Fatal(err)
	}
	sa, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "test-project", "private_key_id": "test-kid",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": "notify@test-project.example.invalid",
		"token_uri":    "https://oauth2.example.invalid/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	ek, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err = x509.MarshalPKCS8PrivateKey(ek)
	if err != nil {
		t.Fatal(err)
	}
	files["/keys/fcm.json"] = sa
	files["/keys/apns.p8"] = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	files["/keys/smsc-login"] = []byte(testSMSCLogin + "\n")
	files["/keys/smsc-password"] = []byte(testSMSCPassword + "\n")
}

func productionEnv(t *testing.T) map[string]string {
	t.Helper()
	env := validEnv(t)
	env["BIRGE_DELIVERY_MODE"] = DeliveryModeProduction
	env["BIRGE_PUSH_PROVIDER"] = "fcm, apns"
	env["BIRGE_FCM_SERVICE_ACCOUNT_FILE"] = "/keys/fcm.json"
	env["BIRGE_APNS_KEY_FILE"] = "/keys/apns.p8"
	env["BIRGE_APNS_KEY_ID"] = "TESTKEY001"
	env["BIRGE_APNS_TEAM_ID"] = "TESTTEAM01"
	env["BIRGE_APNS_TOPIC"] = "kz.example.birge.test"
	env["BIRGE_SMS_PROVIDER"] = "smsc"
	env["BIRGE_SMSC_LOGIN_FILE"] = "/keys/smsc-login"
	env["BIRGE_SMSC_PASSWORD_FILE"] = "/keys/smsc-password"
	env["BIRGE_SMSC_SENDER"] = "BIRGE"
	return env
}

func TestLoadDevKeepsLogSenders(t *testing.T) {
	cfg, err := load(validEnv(t), validFiles(t), CommandWorker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(cfg.PushProviders, ",") != ProviderLog || cfg.SMSProvider != ProviderLog {
		t.Fatalf("dev defaults: push %v sms %q, want log", cfg.PushProviders, cfg.SMSProvider)
	}
	push, sms, err := DeliverySenders(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := push.(LogPushSender); !ok {
		t.Errorf("push sender = %T, want LogPushSender", push)
	}
	if _, ok := sms.(LogSMSSender); !ok {
		t.Errorf("sms sender = %T, want LogSMSSender", sms)
	}
}

func TestLoadProductionProviders(t *testing.T) {
	files := validFiles(t)
	providerFiles(t, files)
	env := productionEnv(t)
	env["BIRGE_APNS_ENVIRONMENT"] = "sandbox"
	env["BIRGE_PUSH_TITLES"] = `{"kk":"TEST kk","ru":"TEST ru","en":"TEST en"}`
	cfg, err := load(env, files, CommandWorker)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FCM.ProjectID != "test-project" || cfg.APNs.KeyID != "TESTKEY001" || cfg.APNsBaseURL != pushadapter.APNsSandboxBaseURL {
		t.Errorf("push credentials: fcm %q apns %q base %q", cfg.FCM.ProjectID, cfg.APNs.KeyID, cfg.APNsBaseURL)
	}
	if cfg.SMSC.Login != testSMSCLogin || cfg.SMSC.Password != testSMSCPassword || cfg.SMSC.Sender != "BIRGE" {
		t.Error("SMSC credentials not loaded from their files")
	}
	if cfg.PushTitles["kk"] != "TEST kk" {
		t.Errorf("push titles = %v", cfg.PushTitles)
	}
	push, sms, err := DeliverySenders(cfg, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := push.(pushadapter.Router)
	if !ok || r.Android == nil || r.IOS == nil {
		t.Errorf("push sender = %#v, want a router with FCM and APNs", push)
	}
	if hs, ok := sms.(*smsadapter.HTTPSender); !ok || hs.Provider.Name() != ProviderSMSC {
		t.Errorf("sms sender = %T, want SMSC", sms)
	}
}

func TestLoadRefusesBadDeliveryConfig(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		files   func(fakeFiles)
		wantErr string
	}{
		{"log push in production", map[string]string{"BIRGE_PUSH_PROVIDER": "log"}, nil, `BIRGE_PUSH_PROVIDER: "log" drops every push`},
		{"log sms in production", map[string]string{"BIRGE_SMS_PROVIDER": "log"}, nil, `BIRGE_SMS_PROVIDER: "log" drops every SMS`},
		{"push provider unset in production", map[string]string{"BIRGE_PUSH_PROVIDER": ""}, nil, "BIRGE_PUSH_PROVIDER is required"},
		{"sms provider unset in production", map[string]string{"BIRGE_SMS_PROVIDER": ""}, nil, "BIRGE_SMS_PROVIDER is required"},
		{"log mixed with fcm", map[string]string{"BIRGE_DELIVERY_MODE": "dev", "BIRGE_PUSH_PROVIDER": "log,fcm"}, nil, "BIRGE_PUSH_PROVIDER"},
		{"unknown push provider", map[string]string{"BIRGE_PUSH_PROVIDER": "webpush"}, nil, "BIRGE_PUSH_PROVIDER"},
		{"duplicate push provider", map[string]string{"BIRGE_PUSH_PROVIDER": "fcm,fcm"}, nil, "listed twice"},
		{"unknown sms provider", map[string]string{"BIRGE_SMS_PROVIDER": "twilio"}, nil, "BIRGE_SMS_PROVIDER"},
		{"fcm file missing", nil, func(f fakeFiles) { delete(f, "/keys/fcm.json") }, "BIRGE_FCM_SERVICE_ACCOUNT_FILE: cannot read"},
		{"fcm file broken", nil, func(f fakeFiles) { f["/keys/fcm.json"] = []byte(`{"type":"service_account"}`) }, "BIRGE_FCM_SERVICE_ACCOUNT_FILE: fcm: the service-account file has no project_id"},
		{"apns key broken", nil, func(f fakeFiles) { f["/keys/apns.p8"] = []byte("TEST-not-a-key") }, "BIRGE_APNS_KEY_FILE"},
		{"apns team id missing", map[string]string{"BIRGE_APNS_TEAM_ID": ""}, nil, "BIRGE_APNS_TEAM_ID is required"},
		{"apns topic missing", map[string]string{"BIRGE_APNS_TOPIC": ""}, nil, "BIRGE_APNS_TOPIC is required"},
		{"apns environment", map[string]string{"BIRGE_APNS_ENVIRONMENT": "staging"}, nil, "BIRGE_APNS_ENVIRONMENT"},
		{"smsc password missing", nil, func(f fakeFiles) { delete(f, "/keys/smsc-password") }, "BIRGE_SMSC_PASSWORD_FILE"},
		{"smsc login empty", nil, func(f fakeFiles) { f["/keys/smsc-login"] = []byte("\n") }, "BIRGE_SMSC_LOGIN_FILE: the secret file is empty"},
		{"bad sender", map[string]string{"BIRGE_SMSC_SENDER": "Too long sender ID"}, nil, "BIRGE_SMSC_SENDER"},
		{"bad push titles", map[string]string{"BIRGE_PUSH_TITLES": `{"de":"Hallo"}`}, nil, "BIRGE_PUSH_TITLES"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := validFiles(t)
			providerFiles(t, files)
			if tt.files != nil {
				tt.files(files)
			}
			env := productionEnv(t)
			for k, v := range tt.set {
				env[k] = v
			}
			_, err := load(env, files, CommandWorker)
			if err == nil {
				t.Fatalf("Load accepted the configuration, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
			for _, secret := range []string{testSMSCPassword, testSMSCLogin, "PRIVATE KEY", "TEST-not-a-key"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error message contains %q", secret)
				}
			}
		})
	}
}

func TestDeliverySendersRefusesLogOutsideDev(t *testing.T) {
	for _, cfg := range []Config{
		{DeliveryMode: DeliveryModeProduction, PushProviders: []string{ProviderLog}, SMSProvider: ProviderSMSC},
		{DeliveryMode: DeliveryModeProduction, PushProviders: []string{ProviderFCM}, SMSProvider: ProviderLog},
		{DeliveryMode: DeliveryModeProduction},
	} {
		if _, _, err := DeliverySenders(cfg, slog.New(slog.DiscardHandler)); err == nil {
			t.Errorf("DeliverySenders accepted push %v sms %q in production", cfg.PushProviders, cfg.SMSProvider)
		}
	}
}
