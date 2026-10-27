package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	membershipmodel "birge/internal/domains/membership/model"
	membershipport "birge/internal/domains/membership/port"
	notifymodel "birge/internal/domains/notify/model"
	notifyport "birge/internal/domains/notify/port"
	privacyport "birge/internal/domains/privacy/port"
	"birge/internal/platform/crypto"
)

// DisplayNameField is the associated-data field of membership.members.display_name,
// the only column the membership context seals. It is part of the schema:
// changing it makes every stored name unreadable (crypto package doc).
const DisplayNameField = "membership.members.display_name"

// DisplayNameEncryptor implements the membership Encryptor port with the
// platform envelope. The membership port carries no field name, so this
// adapter binds every ciphertext to the display_name column of the member's
// own row: AssociatedData(DisplayNameField, memberID). A new sealed column in
// membership needs a port that names the field, not a second adapter guess.
type DisplayNameEncryptor struct {
	Sealer crypto.Sealer
}

var _ membershipport.Encryptor = DisplayNameEncryptor{}

// Seal encrypts a display name of memberID.
func (e DisplayNameEncryptor) Seal(ctx context.Context, memberID uuid.UUID, plaintext []byte) ([]byte, error) {
	return e.Sealer.Seal(ctx, memberID, plaintext, crypto.AssociatedData(DisplayNameField, memberID))
}

// Open decrypts a display name of memberID.
func (e DisplayNameEncryptor) Open(ctx context.Context, memberID uuid.UUID, sealed []byte) ([]byte, error) {
	return e.Sealer.Open(ctx, memberID, sealed, crypto.AssociatedData(DisplayNameField, memberID))
}

// The envelope's Shred has exactly the privacy KeyShredder signature.
var _ privacyport.KeyShredder = (*crypto.Envelope)(nil)

// MemberLookup is the part of the membership service the notify recipients need.
type MemberLookup interface {
	Member(ctx context.Context, id uuid.UUID) (membershipmodel.Member, error)
}

// PhoneLookup returns the phone number of an identity, "" when it has none.
type PhoneLookup interface {
	Phone(ctx context.Context, identityID uuid.UUID) (string, error)
}

// Recipients implements the notify Recipients port for members. Trusted
// contacts (emergency) and staff roles (safety) are not wired yet and are
// reported as model.ErrRecipientUnresolved.
//
// The phone number is looked up on every call and never stored or logged.
// A failing identity provider is an error, so the caller retries; an
// identity without a phone resolves with an empty phone (SMS is skipped).
type Recipients struct {
	Members MemberLookup
	Phones  PhoneLookup
}

var _ notifyport.Recipients = (*Recipients)(nil)

// Resolve resolves a recipient form to its delivery target.
func (rc *Recipients) Resolve(ctx context.Context, r notifymodel.Recipient) ([]notifymodel.ResolvedRecipient, error) {
	if r.MemberID == nil {
		return nil, notifymodel.ErrRecipientUnresolved
	}
	m, err := rc.Members.Member(ctx, *r.MemberID)
	if errors.Is(err, membershipmodel.ErrMemberNotFound) {
		return nil, notifymodel.ErrRecipientUnresolved
	}
	if err != nil {
		return nil, fmt.Errorf("recipients: member %s: %w", *r.MemberID, err)
	}
	phone, err := rc.Phones.Phone(ctx, m.IdentityID)
	if err != nil {
		return nil, fmt.Errorf("recipients: member %s: %w", m.ID, err)
	}
	return []notifymodel.ResolvedRecipient{{
		Kind: notifymodel.RecipientKindMember, Ref: m.ID, PhoneE164: phone, Language: string(m.Lang),
	}}, nil
}

const (
	kratosAdminTimeout  = 5 * time.Second
	maxIdentityBodySize = 64 << 10
)

// KratosIdentities reads identity traits from the Kratos admin API
// (GET <admin>/admin/identities/{id}). The admin API is cluster-internal
// and must never be reachable from outside.
type KratosIdentities struct {
	admin  *url.URL
	client *http.Client
	trait  string
}

// NewKratosIdentities returns a client for the admin API at adminURL that
// reads the phone from trait (default "phone"). Redirects are not followed.
func NewKratosIdentities(adminURL, trait string, client *http.Client) (*KratosIdentities, error) {
	u, err := url.Parse(adminURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("wiring: Kratos admin URL must be an absolute http(s) URL")
	}
	c := &http.Client{Timeout: kratosAdminTimeout}
	if client != nil {
		cp := *client
		c = &cp
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if trait == "" {
		trait = "phone"
	}
	return &KratosIdentities{admin: u, client: c, trait: trait}, nil
}

// Phone returns the phone trait of identityID, "" when the identity does not
// exist or has no phone. Errors carry the identity ID and status only.
func (k *KratosIdentities) Phone(ctx context.Context, identityID uuid.UUID) (string, error) {
	u := k.admin.JoinPath("admin", "identities", identityID.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("kratos admin: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := k.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("kratos admin: identity %s: %w", identityID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body := io.LimitReader(resp.Body, maxIdentityBodySize)
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		_, _ = io.Copy(io.Discard, body)
		return "", nil
	default:
		_, _ = io.Copy(io.Discard, body)
		return "", fmt.Errorf("kratos admin: identity %s: status %d", identityID, resp.StatusCode)
	}
	var identity struct {
		Traits map[string]json.RawMessage `json:"traits"`
	}
	if err := json.NewDecoder(body).Decode(&identity); err != nil {
		// Do not wrap: a decode error may quote the body, which holds personal data.
		return "", fmt.Errorf("kratos admin: identity %s: undecodable response", identityID)
	}
	raw, ok := identity.Traits[k.trait]
	if !ok {
		return "", nil
	}
	var phone string
	if err := json.Unmarshal(raw, &phone); err != nil {
		return "", fmt.Errorf("kratos admin: identity %s: phone trait is not a string", identityID)
	}
	return strings.TrimSpace(phone), nil
}

// LogPushSender is the development push sender: it logs the notification ID
// and the channel and sends nothing. Never the token, never the parameters.
type LogPushSender struct {
	Logger *slog.Logger
}

var _ notifyport.PushSender = LogPushSender{}

// Send logs the push and reports success.
func (s LogPushSender) Send(_ context.Context, msg notifyport.PushMessage) (string, error) {
	s.Logger.Info("log-only delivery", "channel", notifymodel.ChannelPush, "notification_id", msg.NotificationID)
	return "", nil
}

// LogSMSSender is the development SMS sender: it logs the notification ID
// and the channel and sends nothing. Never the phone number, never the text.
type LogSMSSender struct {
	Logger *slog.Logger
}

var _ notifyport.SMSSender = LogSMSSender{}

// Send logs the SMS and reports success.
func (s LogSMSSender) Send(_ context.Context, msg notifyport.SMSMessage) (string, error) {
	s.Logger.Info("log-only delivery", "channel", notifymodel.ChannelSMS, "notification_id", msg.NotificationID)
	return "", nil
}
