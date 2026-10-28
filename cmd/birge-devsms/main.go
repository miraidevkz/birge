// Command birge-devsms is the SMS endpoint of the development Kratos courier
// in deploy/compose. By default it only accepts and drops each message, like
// the unreachable sink it replaces (testers read codes with `up.sh codes`).
//
// With BIRGE_DEVSMS_TELEGRAM=1 and a Telegram Gateway token it delivers the
// sign-in code through Telegram's Verification Gateway instead
// (https://core.telegram.org/gateway): sendVerificationMessage with the
// member's phone number and the code Kratos generated.
//
// Development and testing only, by the owner's decision of 2026-10-28, and
// never a production channel: it sends the phone number to a third-party
// messenger outside the deployment's jurisdiction, which non-negotiable 3
// rules out for real members. Testers must know their number goes to
// Telegram. Production uses an SMS provider plugin in the jurisdiction.
//
// Behaviour:
//   - Only numbers with an allowed prefix (BIRGE_SMS_ALLOWED_PREFIXES,
//     default +77) are sent; others are dropped.
//   - The code is the run of 4 to 8 digits in the courier body
//     ("Birge: 123456"); a body without one is dropped.
//   - One Gateway call per message and the answer is always 200: Kratos
//     retries failed deliveries, and every Gateway call to a number other than
//     the token owner's is paid, so a retry must never send twice.
//   - Logs carry only the last two digits of the number, never the code.
//
// Configuration: LISTEN (default :4000), BIRGE_DEVSMS_TELEGRAM,
// BIRGE_DEVSMS_TOKEN_FILE (the Gateway API token), BIRGE_DEVSMS_GATEWAY_URL
// (default https://gatewayapi.telegram.org), BIRGE_SMS_ALLOWED_PREFIXES.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// courierMessage is the body the Kratos courier sends (kratos.yml jsonnet).
type courierMessage struct {
	Recipient string `json:"recipient"`
	Body      string `json:"body"`
}

var (
	codeRe  = regexp.MustCompile(`\b(\d{4,8})\b`)
	e164Re  = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)
	maxBody = int64(4 << 10)
)

// gateway delivers one code; nil means drop (log only).
type gateway struct {
	url    string
	token  string
	client *http.Client
}

type server struct {
	gw       *gateway
	prefixes []string
	log      *slog.Logger
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	s := &server{log: log, prefixes: prefixesFrom(os.Getenv("BIRGE_SMS_ALLOWED_PREFIXES"))}
	if os.Getenv("BIRGE_DEVSMS_TELEGRAM") == "1" {
		token, err := readToken(os.Getenv("BIRGE_DEVSMS_TOKEN_FILE"))
		if err != nil {
			log.Error("telegram delivery needs BIRGE_DEVSMS_TOKEN_FILE", "error", err)
			os.Exit(2)
		}
		url := strings.TrimRight(os.Getenv("BIRGE_DEVSMS_GATEWAY_URL"), "/")
		if url == "" {
			url = "https://gatewayapi.telegram.org"
		}
		s.gw = &gateway{url: url, token: token, client: &http.Client{Timeout: 10 * time.Second}}
		log.Warn("development SMS relay sends sign-in codes through Telegram Gateway: testing only",
			"event", "DevSMSTelegramEnabled")
	}
	addr := os.Getenv("LISTEN")
	if addr == "" {
		addr = ":4000"
	}
	srv := &http.Server{Addr: addr, Handler: newMux(s), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

// newMux registers the courier endpoint and the health check.
func newMux(s *server) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sms", s.sms)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	return mux
}

func readToken(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("no token file")
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: the operator names the token file
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	t := strings.TrimSpace(string(b))
	if t == "" {
		return "", fmt.Errorf("token file is empty")
	}
	return t, nil
}

func prefixesFrom(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"+77"}
	}
	return out
}

// tail is the last two digits of a number, for logs.
func tail(phone string) string {
	if len(phone) < 2 {
		return ""
	}
	return phone[len(phone)-2:]
}

func (s *server) sms(w http.ResponseWriter, r *http.Request) {
	// Always 200: see the package comment (no paid double sends on retry).
	defer w.WriteHeader(http.StatusOK)
	var m courierMessage
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&m); err != nil {
		s.log.Warn("courier message unreadable", "event", "DevSMSDropped", "reason", "bad_json")
		return
	}
	phone := strings.TrimSpace(m.Recipient)
	if !e164Re.MatchString(phone) || !s.allowed(phone) {
		s.log.Info("number not allowed", "event", "DevSMSDropped", "reason", "prefix", "tail", tail(phone))
		return
	}
	code := codeRe.FindString(m.Body)
	if code == "" {
		s.log.Info("no code in body", "event", "DevSMSDropped", "reason", "no_code", "tail", tail(phone))
		return
	}
	if s.gw == nil {
		s.log.Info("code kept for up.sh codes", "event", "DevSMSDropped", "reason", "telegram_off", "tail", tail(phone))
		return
	}
	id, err := s.gw.send(r.Context(), phone, code)
	if err != nil {
		s.log.Warn("telegram gateway refused", "event", "DevSMSTelegramFailed", "tail", tail(phone), "error", err)
		return
	}
	s.log.Info("code sent through telegram", "event", "DevSMSTelegramSent", "tail", tail(phone), "request_id", id)
}

func (s *server) allowed(phone string) bool {
	for _, p := range s.prefixes {
		if strings.HasPrefix(phone, p) {
			return true
		}
	}
	return false
}

// send calls sendVerificationMessage with our code; Telegram does not
// generate one, so the code the member types is the one Kratos checks.
func (g *gateway) send(ctx context.Context, phone, code string) (string, error) {
	body, err := json.Marshal(map[string]any{"phone_number": phone, "code": code, "ttl": 600})
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.url+"/sendVerificationMessage", bytes.NewReader(body)) //nolint:gosec // G704: host comes from configuration only
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.token)
	resp, err := g.client.Do(req) //nolint:gosec // G704: see above
	if err != nil {
		return "", fmt.Errorf("gateway: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Result struct {
			RequestID string `json:"request_id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", fmt.Errorf("gateway: %s, bad response: %w", resp.Status, err)
	}
	if !out.OK {
		return "", fmt.Errorf("gateway: %s", out.Error)
	}
	return out.Result.RequestID, nil
}
