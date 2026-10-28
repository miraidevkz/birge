package smsgw

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"birge/internal/domains/notify/port"
)

// DefaultSMSCBaseURL is the SMSC.kz host.
const DefaultSMSCBaseURL = "https://smsc.kz"

// SMSC is the SMSC.kz HTTP API (https://smsc.kz/api/http/send/sms/):
// POST /sys/send.php with fmt=3 (JSON reply) and charset=utf-8. It holds the
// account password: never log it (String redacts it).
type SMSC struct {
	// BaseURL overrides DefaultSMSCBaseURL (tests point it at httptest).
	BaseURL  string
	Login    string
	Password string
	// Sender is the registered sender ID; empty uses the account default.
	Sender string
}

var _ Provider = SMSC{}

// String redacts the credentials.
func (SMSC) String() string { return "smsgw.SMSC{redacted}" }

// GoString redacts the credentials for %#v.
func (SMSC) GoString() string { return "smsgw.SMSC{redacted}" }

// Name implements Provider.
func (SMSC) Name() string { return "smsc" }

// Request implements Provider. Everything goes in the POST body: a GET
// would put the password and the phone into URLs that proxies log.
func (p SMSC) Request(ctx context.Context, msg port.SMSMessage) (*http.Request, error) {
	if msg.PhoneE164 == "" || msg.Text == "" {
		return nil, fmt.Errorf("empty phone or text: %w", port.ErrPermanent)
	}
	form := url.Values{
		"login":   {p.Login},
		"psw":     {p.Password},
		"phones":  {strings.TrimPrefix(msg.PhoneE164, "+")},
		"mes":     {msg.Text},
		"fmt":     {"3"},
		"charset": {"utf-8"},
	}
	if p.Sender != "" {
		form.Set("sender", p.Sender)
	}
	base := p.BaseURL
	if base == "" {
		base = DefaultSMSCBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/sys/send.php", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("smsc: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req, nil
}

// Parse implements Provider. SMSC answers 200 for both outcomes: {"id": N,
// "cnt": N} on success, {"error": "...", "error_code": N} on failure. The
// error text is not used: it is free text and may echo request data.
func (SMSC) Parse(status int, body []byte) (string, error) {
	if status != http.StatusOK {
		// Not an API answer (a proxy or a moved endpoint): our side or
		// theirs, never this message. Retry.
		return "", fmt.Errorf("smsc: status %d", status)
	}
	var reply struct {
		ID        json.RawMessage `json:"id"`
		ErrorCode int             `json:"error_code"`
		Error     string          `json:"error"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return "", errors.New("smsc: reply is not JSON")
	}
	if reply.ErrorCode != 0 || reply.Error != "" {
		return "", smscError(reply.ErrorCode)
	}
	id := strings.Trim(string(reply.ID), `"`)
	if id == "" || id == "null" {
		return "", errors.New("smsc: reply has neither id nor error")
	}
	return id, nil
}

// smscError maps the documented send.php error codes. Codes that describe
// this message (its number, text or parameters) are permanent; codes about
// the account or the gateway are retried, because an operator fixes them
// (credentials, balance, an IP block) and the critical SMS must then still
// go out. Unknown codes are retried for the same reason.
func smscError(code int) error {
	base := fmt.Sprintf("smsc: error code %d", code)
	switch code {
	case 1, // error in the parameters
		5, // wrong date format (we never send a date)
		6, // message forbidden (text or sender ID)
		7, // invalid phone number format
		8, // the message cannot be delivered to this number
		// 9: an identical send request within the last minute. SMSC
		// already has this message (an earlier attempt whose reply was
		// lost), so retrying would only risk a duplicate SMS.
		9:
		return fmt.Errorf("%s: %w", base, port.ErrPermanent)
	default: // 2 login/password, 3 balance, 4 IP blocked, unknown
		return errors.New(base)
	}
}
