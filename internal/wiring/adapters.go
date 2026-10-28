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

	emergencymodel "birge/internal/domains/emergency/model"
	emergencyport "birge/internal/domains/emergency/port"
	emergencyservice "birge/internal/domains/emergency/service"
	membershipmodel "birge/internal/domains/membership/model"
	membershipport "birge/internal/domains/membership/port"
	notifymodel "birge/internal/domains/notify/model"
	notifyport "birge/internal/domains/notify/port"
	privacyport "birge/internal/domains/privacy/port"
	safetymodel "birge/internal/domains/safety/model"
	safetyport "birge/internal/domains/safety/port"
	safetyservice "birge/internal/domains/safety/service"
	wellbeingport "birge/internal/domains/wellbeing/port"
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

// Envelope is also the Sealer for safety (notes, duty phones), wellbeing
// (check-in answers) and emergency (SOS text, child card, contact name/phone):
// each call already carries AssociatedData for its column.
var (
	_ safetyport.Sealer    = (*crypto.Envelope)(nil)
	_ wellbeingport.Sealer = (*crypto.Envelope)(nil)
	_ emergencyport.Sealer = (*crypto.Envelope)(nil)
)

// MemberLookup is the part of the membership service the notify recipients need.
type MemberLookup interface {
	Member(ctx context.Context, id uuid.UUID) (membershipmodel.Member, error)
}

// PhoneLookup returns the phone number of an identity, "" when it has none.
type PhoneLookup interface {
	Phone(ctx context.Context, identityID uuid.UUID) (string, error)
}

// StaffDirectory is the part of safety notify needs for staff_role recipients.
type StaffDirectory interface {
	ResolveStaffRole(ctx context.Context, role string) ([]safetyservice.ExternalRecipient, error)
	ShiftPhone(ctx context.Context, shiftID uuid.UUID) (string, error)
}

// TrustedContacts is the part of emergency notify needs for trusted_contact_id.
type TrustedContacts interface {
	TrustedContactTarget(ctx context.Context, id uuid.UUID) (memberID *uuid.UUID, external bool, err error)
	ContactPhone(ctx context.Context, trustedContactID uuid.UUID) (phone, lang string, err error)
}

// Recipients implements the notify Recipients port. Members resolve via
// membership; staff_role via safety; trusted_contact_id via emergency.
//
// Resolve uses membership / safety / emergency only (kind, ref, language) so a
// Kratos outage cannot block consuming DeliveryRequested. PhoneFor calls
// Kratos (members), safety.ShiftPhone (staff) or emergency.ContactPhone
// (external trusted contacts) at SMS send time, outside any database
// transaction. Phones are never stored or logged.
type Recipients struct {
	Members   MemberLookup
	Phones    PhoneLookup
	Staff     StaffDirectory
	Emergency TrustedContacts
	StaffLang string // language for StaffShiftID re-resolve (BIRGE_STAFF_SMS_LANG)
}

var _ notifyport.Recipients = (*Recipients)(nil)

// Resolve resolves a recipient form to kind, ref and language. It does not
// look up phones from the identity provider.
func (rc *Recipients) Resolve(ctx context.Context, r notifymodel.Recipient) ([]notifymodel.ResolvedRecipient, error) {
	switch {
	case r.MemberID != nil:
		m, err := rc.Members.Member(ctx, *r.MemberID)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		if err != nil {
			return nil, fmt.Errorf("recipients: member %s: %w", *r.MemberID, err)
		}
		return []notifymodel.ResolvedRecipient{{
			Kind: notifymodel.RecipientKindMember, Ref: m.ID, Language: string(m.Lang),
		}}, nil
	case r.StaffRole != "":
		if rc.Staff == nil {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		shifts, err := rc.Staff.ResolveStaffRole(ctx, r.StaffRole)
		if errors.Is(err, safetymodel.ErrNoDutyCoverage) {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		if err != nil {
			return nil, fmt.Errorf("recipients: staff role: %w", err)
		}
		out := make([]notifymodel.ResolvedRecipient, 0, len(shifts))
		for _, s := range shifts {
			lang := s.Lang
			if lang == "" {
				lang = rc.staffLang()
			}
			out = append(out, notifymodel.ResolvedRecipient{
				Kind: notifymodel.RecipientKindStaff, Ref: s.Ref, Language: lang,
			})
		}
		if len(out) == 0 {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		return out, nil
	case r.StaffShiftID != nil:
		// Re-resolve language at SMS send time; phone comes from PhoneFor.
		if *r.StaffShiftID == uuid.Nil {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		return []notifymodel.ResolvedRecipient{{
			Kind: notifymodel.RecipientKindStaff, Ref: *r.StaffShiftID, Language: rc.staffLang(),
		}}, nil
	case r.TrustedContactID != nil:
		if rc.Emergency == nil || *r.TrustedContactID == uuid.Nil {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		memberID, external, err := rc.Emergency.TrustedContactTarget(ctx, *r.TrustedContactID)
		if errors.Is(err, emergencyservice.ErrNotFound) {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		if err != nil {
			return nil, fmt.Errorf("recipients: trusted contact %s: %w", *r.TrustedContactID, err)
		}
		if external {
			// Language comes from the contact row; phone is opened only in PhoneFor.
			_, lang, err := rc.Emergency.ContactPhone(ctx, *r.TrustedContactID)
			if errors.Is(err, emergencyservice.ErrNotFound) {
				return nil, notifymodel.ErrRecipientUnresolved
			}
			if err != nil {
				return nil, fmt.Errorf("recipients: trusted contact %s: %w", *r.TrustedContactID, err)
			}
			return []notifymodel.ResolvedRecipient{{
				Kind: notifymodel.RecipientKindExternal, Ref: *r.TrustedContactID, Language: lang,
			}}, nil
		}
		if memberID == nil || *memberID == uuid.Nil {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		m, err := rc.Members.Member(ctx, *memberID)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			return nil, notifymodel.ErrRecipientUnresolved
		}
		if err != nil {
			return nil, fmt.Errorf("recipients: trusted contact member %s: %w", *memberID, err)
		}
		return []notifymodel.ResolvedRecipient{{
			Kind: notifymodel.RecipientKindMember, Ref: m.ID, Language: string(m.Lang),
		}}, nil
	default:
		return nil, notifymodel.ErrRecipientUnresolved
	}
}

func (rc *Recipients) staffLang() string {
	if rc.StaffLang != "" {
		return rc.StaffLang
	}
	return defaultStaffSMSLang
}

// PhoneFor returns the E.164 phone for a stored recipient.
func (rc *Recipients) PhoneFor(ctx context.Context, kind string, ref uuid.UUID) (string, error) {
	switch kind {
	case notifymodel.RecipientKindMember:
		if ref == uuid.Nil {
			return "", notifymodel.ErrRecipientUnresolved
		}
		m, err := rc.Members.Member(ctx, ref)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			return "", notifymodel.ErrRecipientUnresolved
		}
		if err != nil {
			return "", fmt.Errorf("recipients: member %s: %w", ref, err)
		}
		phone, err := rc.Phones.Phone(ctx, m.IdentityID)
		if err != nil {
			return "", fmt.Errorf("recipients: member %s: %w", m.ID, err)
		}
		return phone, nil
	case notifymodel.RecipientKindStaff:
		if rc.Staff == nil || ref == uuid.Nil {
			return "", notifymodel.ErrRecipientUnresolved
		}
		phone, err := rc.Staff.ShiftPhone(ctx, ref)
		if errors.Is(err, safetymodel.ErrNotFound) {
			// Cancelled or unknown shift: skip the SMS rather than retry forever.
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("recipients: staff shift %s: %w", ref, err)
		}
		return phone, nil
	case notifymodel.RecipientKindExternal:
		if rc.Emergency == nil || ref == uuid.Nil {
			return "", notifymodel.ErrRecipientUnresolved
		}
		phone, _, err := rc.Emergency.ContactPhone(ctx, ref)
		if errors.Is(err, emergencyservice.ErrNotFound) {
			return "", notifymodel.ErrRecipientUnresolved
		}
		if err != nil {
			return "", fmt.Errorf("recipients: trusted contact %s: %w", ref, err)
		}
		return phone, nil
	default:
		return "", notifymodel.ErrRecipientUnresolved
	}
}

// PlanLinkMinter is the part of emergency the plan SMS link needs.
type PlanLinkMinter interface {
	ExternalPlanLink(ctx context.Context, trustedContactID uuid.UUID) (string, error)
}

// PlanSMSLinks implements notify's SMSLinks port: the emergency.plan_sent SMS
// of an external trusted contact carries PublicURL + "/plan#" + token. The
// token is in the fragment, so it never reaches a server or an access log
// when the link is opened (the page posts it to viewExternalPlan). The link
// is returned to notify only for the SMS body; nothing here logs it.
type PlanSMSLinks struct {
	Emergency PlanLinkMinter
	// PublicURL is the member web app origin (BIRGE_PUBLIC_URL), without a
	// trailing slash.
	PublicURL string
}

var _ notifyport.SMSLinks = (*PlanSMSLinks)(nil)

// LinkFor mints the plan link for an external contact's emergency.plan_sent
// SMS; any other template or recipient has no link (ErrNotFound).
func (p *PlanSMSLinks) LinkFor(ctx context.Context, template, kind string, ref uuid.UUID) (string, error) {
	if template != emergencymodel.TemplatePlanSent || kind != notifymodel.RecipientKindExternal || ref == uuid.Nil ||
		p.Emergency == nil || p.PublicURL == "" {
		return "", notifymodel.ErrNotFound
	}
	token, err := p.Emergency.ExternalPlanLink(ctx, ref)
	if errors.Is(err, emergencyservice.ErrNotFound) {
		return "", notifymodel.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("plan sms link: trusted contact %s: %w", ref, err)
	}
	return strings.TrimRight(p.PublicURL, "/") + "/plan#" + token, nil
}

// EmergencyMembers implements emergency's MemberInfo from membership.
// DisplayName is the first name only (contract: never address or location):
// membership's full display name is split at the first space.
type EmergencyMembers struct {
	Members MemberLookup
}

var _ emergencyport.MemberInfo = (*EmergencyMembers)(nil)

// Profiles returns verification, district, first name and language for each id.
// Unknown members are absent. Results are never logged.
func (d *EmergencyMembers) Profiles(ctx context.Context, memberIDs []uuid.UUID) (map[uuid.UUID]emergencymodel.Profile, error) {
	out := make(map[uuid.UUID]emergencymodel.Profile, len(memberIDs))
	for _, id := range memberIDs {
		if id == uuid.Nil {
			continue
		}
		m, err := d.Members.Member(ctx, id)
		if errors.Is(err, membershipmodel.ErrMemberNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("emergency members: %w", err)
		}
		out[id] = emergencymodel.Profile{
			Verified:    m.Verification == membershipmodel.VerificationVerified,
			DistrictID:  m.DistrictID,
			DisplayName: firstName(m.DisplayName),
			Lang:        string(m.Lang),
		}
	}
	return out, nil
}

// firstName returns the part of display before the first space, or display
// when it has no space. Empty input stays empty.
func firstName(display string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(display), " ")
	return first
}

// EmergencyBlocks implements emergency's Blocks from membership IsBlocked.
type EmergencyBlocks struct {
	Members interface {
		IsBlocked(ctx context.Context, a, b uuid.UUID) (bool, error)
	}
}

var _ emergencyport.Blocks = (*EmergencyBlocks)(nil)

// IsBlocked reports whether a blocked b or b blocked a.
func (b *EmergencyBlocks) IsBlocked(ctx context.Context, a, bID uuid.UUID) (bool, error) {
	return b.Members.IsBlocked(ctx, a, bID)
}

// MemberDirectory implements safety's MemberDirectory from membership and Kratos.
// Contact results are never logged.
type MemberDirectory struct {
	Members MemberLookup
	Phones  PhoneLookup
}

var _ safetyport.MemberDirectory = (*MemberDirectory)(nil)

// Contact returns the member's display name, language and phone.
func (d *MemberDirectory) Contact(ctx context.Context, memberID uuid.UUID) (safetymodel.Contact, error) {
	m, err := d.Members.Member(ctx, memberID)
	if errors.Is(err, membershipmodel.ErrMemberNotFound) {
		return safetymodel.Contact{}, safetymodel.ErrNotFound
	}
	if err != nil {
		return safetymodel.Contact{}, fmt.Errorf("member directory: %w", err)
	}
	phone, err := d.Phones.Phone(ctx, m.IdentityID)
	if err != nil {
		return safetymodel.Contact{}, fmt.Errorf("member directory: %w", err)
	}
	return safetymodel.Contact{
		DisplayName: m.DisplayName,
		Lang:        string(m.Lang),
		Phone:       phone,
	}, nil
}

// District returns the member's district, or nil when unset.
func (d *MemberDirectory) District(ctx context.Context, memberID uuid.UUID) (*uuid.UUID, error) {
	m, err := d.Members.Member(ctx, memberID)
	if errors.Is(err, membershipmodel.ErrMemberNotFound) {
		return nil, safetymodel.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("member directory: %w", err)
	}
	return m.DistrictID, nil
}

// CrisisOpener adapts safety.OpenEscalation to the wellbeing CrisisOpener port
// (which does not need the alreadyOpen flag).
type CrisisOpener struct {
	Safety *safetyservice.Service
}

var _ wellbeingport.CrisisOpener = (*CrisisOpener)(nil)

// OpenEscalation opens a human escalation for the member (source typically "checkin").
func (o *CrisisOpener) OpenEscalation(ctx context.Context, memberID uuid.UUID, source string) (uuid.UUID, error) {
	if o == nil || o.Safety == nil {
		return uuid.Nil, errors.New("wiring: crisis opener not configured")
	}
	id, _, err := o.Safety.OpenEscalation(ctx, memberID, source)
	return id, err
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

// DeleteIdentity deletes identityID (DELETE <admin>/admin/identities/{id}).
// 204 and 404 both mean the identity is gone; anything else is an error
// that carries the identity ID and status only, so the timer retries.
func (k *KratosIdentities) DeleteIdentity(ctx context.Context, identityID uuid.UUID) error {
	u := k.admin.JoinPath("admin", "identities", identityID.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return fmt.Errorf("kratos admin: %w", err)
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return fmt.Errorf("kratos admin: delete identity %s: %w", identityID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxIdentityBodySize))
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("kratos admin: delete identity %s: status %d", identityID, resp.StatusCode)
	}
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
