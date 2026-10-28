package authgw

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"birge/internal/platform/httpapi"
)

// CourierPath is where the internal listener mounts the Kratos courier hook.
// It is not in the public contract and must never be routed by the ingress.
const CourierPath = "/internal/kratos/courier"

// Kratos template types the hook accepts: the SMS code of the login and the
// registration flow, the only messages members need. Every other template
// (verification, recovery, anything added later) is refused.
const (
	TemplateLoginCode        = "login_code_valid"
	TemplateRegistrationCode = "registration_code_valid"
)

const (
	courierMaxBody        = 4 << 10
	courierSendTimeout    = 15 * time.Second
	minCourierSecretBytes = 32
)

// Errors a CodeSender returns so the hook can answer Kratos. Wrap them with
// %w; any other error is treated as transient.
var (
	// ErrCodeRefused means the SMS guard refused the send (number range,
	// rate limit, daily budget). Kratos gets 429 and retries later.
	ErrCodeRefused = errors.New("authgw: sign-in SMS refused")
	// ErrCodePermanent means the provider refused this message for good
	// (bad number). Kratos gets 422.
	ErrCodePermanent = errors.New("authgw: sign-in SMS rejected by the provider")
)

// CodeSender hands one sign-in code to the SMS path. Implemented in
// internal/wiring by notify's synchronous SendSignInCode, which applies the
// SMS guard and never stores the code. Implementations must not log, store or
// publish phoneE164 or code.
type CodeSender interface {
	SendSignInCode(ctx context.Context, phoneE164, code string) error
}

// CourierConfig configures the Kratos courier hook.
type CourierConfig struct {
	// Secret is the shared secret Kratos sends as "Authorization: Bearer
	// <secret>", at least 32 bytes, from a file (BIRGE_COURIER_SECRET_FILE).
	Secret []byte
	// AllowedPrefixes are the E.164 prefixes of allowed mobile ranges, the
	// same list the gateway and the SMS guard use.
	AllowedPrefixes []string
	// Sender delivers the code.
	Sender CodeSender
	Logger *slog.Logger
}

// CourierHook is the endpoint of Kratos' HTTP courier for sign-in codes
// (ADR-0008 addendum): Kratos generates the code and calls this hook, which
// sends it through notify's SMS path with the SMS guard.
//
// The request body is the one deploy/compose/kratos/courier-hook.jsonnet
// builds: {"to": E.164, "template": Kratos template type, "code": digits}.
// Only the code is taken from Kratos; the SMS text is fixed in notify, so
// nothing else Kratos knows (identity traits, the rendered body) reaches the
// provider.
//
// Answers: 204 sent; 401 missing or wrong secret; 400 malformed body or
// code; 422 template not allowed, number invalid or not in an allowed range,
// or refused for good by the provider; 429 refused by the SMS guard; 503 the
// provider failed transiently. Kratos treats every non-2xx as a failure and
// retries the message, up to its courier retry limit.
//
// Logs carry the outcome and template only, never the number, the code or
// the secret.
type CourierHook struct {
	secretDigest [sha256.Size]byte
	prefixes     []string
	sender       CodeSender
	log          *slog.Logger
}

// NewCourierHook validates cfg and builds the hook.
func NewCourierHook(cfg CourierConfig) (*CourierHook, error) {
	switch {
	case len(cfg.Secret) < minCourierSecretBytes:
		return nil, errors.New("authgw: courier secret must be at least 32 bytes")
	case len(cfg.AllowedPrefixes) == 0:
		return nil, errors.New("authgw: AllowedPrefixes is empty")
	case cfg.Sender == nil:
		return nil, errors.New("authgw: courier hook needs a CodeSender")
	}
	for _, p := range cfg.AllowedPrefixes {
		if len(p) < 2 || p[0] != '+' || strings.Trim(p[1:], "0123456789") != "" {
			return nil, errors.New("authgw: AllowedPrefixes must be '+' followed by digits")
		}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &CourierHook{
		secretDigest: sha256.Sum256(cfg.Secret),
		prefixes:     append([]string(nil), cfg.AllowedPrefixes...),
		sender:       cfg.Sender,
		log:          cfg.Logger,
	}, nil
}

type courierMessage struct {
	To       string `json:"to"`
	Template string `json:"template"`
	Code     string `json:"code"`
}

// ServeHTTP handles one courier delivery.
func (h *CourierHook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		httpapi.WriteProblem(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return
	}
	if !h.authorized(r.Header.Get("Authorization")) {
		h.refuse(w, http.StatusUnauthorized, "unauthorized", "", "secret")
		return
	}
	var msg courierMessage
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, courierMaxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&msg); err != nil || dec.More() {
		h.refuse(w, http.StatusBadRequest, "invalid_request", "", "body")
		return
	}
	if msg.Template != TemplateLoginCode && msg.Template != TemplateRegistrationCode {
		// An unexpected value is not logged: it is not one of ours.
		h.refuse(w, http.StatusUnprocessableEntity, "template_not_allowed", "", "template")
		return
	}
	if err := checkNumber(msg.To, h.prefixes); err != nil {
		h.refuse(w, http.StatusUnprocessableEntity, "invalid_phone", msg.Template, err.Error())
		return
	}
	if !validCode(msg.Code) {
		h.refuse(w, http.StatusBadRequest, "invalid_code", msg.Template, "code")
		return
	}

	// Kratos waits for the answer; bound it so a hanging provider frees
	// the courier, which then retries.
	ctx, cancel := context.WithTimeout(r.Context(), courierSendTimeout)
	defer cancel()
	err := h.sender.SendSignInCode(ctx, msg.To, msg.Code)
	switch {
	case err == nil:
		h.log.Info("courier hook sent sign-in code", "event", "SignInCodeSent", "template", msg.Template)
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, ErrCodeRefused):
		w.Header().Set("Retry-After", "60")
		h.refuse(w, http.StatusTooManyRequests, "sms_refused", msg.Template, "guard")
	case errors.Is(err, ErrCodePermanent):
		h.refuse(w, http.StatusUnprocessableEntity, "sms_rejected", msg.Template, "provider_permanent")
	default:
		// The error text comes from the sender, whose contract keeps the
		// phone and the code out of it; it is still not logged here, the
		// sender logs its own failure with an ID.
		h.refuse(w, http.StatusServiceUnavailable, "sms_unavailable", msg.Template, "provider")
	}
}

// authorized compares the bearer secret in constant time. Both sides are
// hashed first so the comparison takes the same time whatever the length of
// the presented value.
func (h *CourierHook) authorized(header string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], h.secretDigest[:]) == 1
}

func (h *CourierHook) refuse(w http.ResponseWriter, status int, code, template, reason string) {
	attrs := []any{"event", "SignInCodeRefused", "status", status, "reason", reason}
	if template != "" {
		attrs = append(attrs, "template", template)
	}
	h.log.Info("courier hook refused message", attrs...)
	httpapi.WriteProblem(w, status, code, "The message was not sent.")
}

// validCode accepts 4 to 10 ASCII digits, the shape of a Kratos code; notify
// checks it again.
func validCode(code string) bool {
	if len(code) < 4 || len(code) > 10 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}
