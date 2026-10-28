// Package model holds the circle domain types and invariants: tea requests,
// matches, acquaintances, scoring inputs, events and errors. No I/O.
//
// Names, districts, ages and needs appear here only while building a match
// card for the caller; they never go into events, logs or stored rows.
package model

import (
	"bytes"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Formats a mother may choose for Tea (§ 4.4).
const (
	FormatMeeting = "meeting"
	FormatCall    = "call"
)

// Tea request statuses.
const (
	RequestOpen      = "open"
	RequestMatched   = "matched"
	RequestCancelled = "cancelled"
	RequestExpired   = "expired"
)

// Match statuses (contract Match.status).
const (
	MatchProposed = "proposed"
	MatchAccepted = "accepted"
	MatchDeclined = "declined"
	MatchMet      = "met"
	MatchNotMet   = "not_met"
	MatchExpired  = "expired"
)

// Defaults from § 4.4 / config.
const (
	DefaultRequestTTL      = 7 * 24 * time.Hour
	DefaultMatchInterval   = time.Hour
	DefaultAskMetAfter     = 3 * 24 * time.Hour
	DefaultRematchCooldown = 30 * 24 * time.Hour
	DefaultWeightDistrict  = 3
	DefaultWeightAge       = 2
	DefaultWeightNeed      = 2
	DefaultWeightLanguage  = 1
	AgeWindowYears         = 2
	MaxTeaRequestsListed   = 20
	MaxMatchesListed       = 50
	MaxAcquaintancesListed = 500
)

// Events published by circle, and the notify event it asks for.
const (
	EventTeaRequested            = "circle.TeaRequested"
	EventMatchAccepted           = "circle.MatchAccepted"
	EventAcquaintanceEstablished = "circle.AcquaintanceEstablished"
	EventDeliveryRequested       = "notify.DeliveryRequested"
	EventVersion                 = 1
)

// Notify templates and delivery class.
const (
	TemplateMatchProposed = "circle.match_proposed"
	TemplateAskMet        = "circle.ask_met"
	ClassNormal           = "normal"
)

// Platform timer kinds (ADR-0020).
const (
	// TimerMatchRun pairs open tea requests hourly and re-schedules itself.
	TimerMatchRun = "circle.MatchRun"
	// TimerRequestExpire expires one open tea request (subject = request id).
	TimerRequestExpire = "circle.RequestExpire"
	// TimerAskMet asks both members whether they met (subject = match id).
	TimerAskMet = "circle.AskMet"
)

// MatchRunSubject is the fixed subject_id of the recurring MatchRun timer.
// Timers require a non-nil subject; the job is global, not per member.
var MatchRunSubject = uuid.MustParse("01900000-0000-7000-8000-00000000c001")

var (
	// ErrUnauthorized means there is no member session.
	ErrUnauthorized = errors.New("circle: unauthorized")
	// ErrForbidden means the member may not do this (not verified).
	ErrForbidden = errors.New("circle: forbidden")
	// ErrNotFound means the object does not exist or is not visible to the caller.
	ErrNotFound = errors.New("circle: not found")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("circle: invalid input")
	// ErrConflict means the current state does not allow the operation
	// (already an open request, wrong match state).
	ErrConflict = errors.New("circle: conflict")
)

// FormatIsValid reports whether f is a known tea format.
func FormatIsValid(f string) bool {
	return f == FormatMeeting || f == FormatCall
}

// CanonicalPair returns (lo, hi) with lo < hi by UUID byte order (matches
// PostgreSQL uuid ordering used by the table checks).
func CanonicalPair(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	if bytes.Compare(a[:], b[:]) <= 0 {
		return a, b
	}
	return b, a
}

// Preferences are hard filters when true (§ 4.4).
type Preferences struct {
	SameDistrict bool
	SimilarAge   bool
	SharedNeeds  bool
}

// TeaRequest is one stored tea request.
type TeaRequest struct {
	ID             uuid.UUID
	MemberID       uuid.UUID
	IdempotencyKey uuid.UUID
	Format         string
	Preferences    Preferences
	Status         string
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// Match is one stored pair proposal.
type Match struct {
	ID         uuid.UUID
	MemberA    uuid.UUID
	MemberB    uuid.UUID
	RequestAID uuid.UUID
	RequestBID uuid.UUID
	Format     string
	AnswerA    *bool
	AnswerB    *bool
	MetA       *bool
	MetB       *bool
	Status     string
	RoomID     *uuid.UUID
	AskMetAt   *time.Time
	CreatedAt  time.Time
}

// OtherMember returns the member of m who is not viewer.
func (m Match) OtherMember(viewer uuid.UUID) (uuid.UUID, bool) {
	switch viewer {
	case m.MemberA:
		return m.MemberB, true
	case m.MemberB:
		return m.MemberA, true
	default:
		return uuid.Nil, false
	}
}

// MyAnswer returns the viewer's accept answer, if any.
func (m Match) MyAnswer(viewer uuid.UUID) *bool {
	switch viewer {
	case m.MemberA:
		return m.AnswerA
	case m.MemberB:
		return m.AnswerB
	default:
		return nil
	}
}

// Acquaintance is a confirmed "met in person" pair (§ 3).
type Acquaintance struct {
	MemberA uuid.UUID
	MemberB uuid.UUID
	Since   time.Time
}

// Other returns the member of the pair who is not viewer.
func (a Acquaintance) Other(viewer uuid.UUID) (uuid.UUID, bool) {
	switch viewer {
	case a.MemberA:
		return a.MemberB, true
	case a.MemberB:
		return a.MemberA, true
	default:
		return uuid.Nil, false
	}
}

// Child is what circle needs about a member's child for scoring and cards.
// No names.
type Child struct {
	BirthYear int
	Needs     []string
}

// Profile is what circle reads about a member from membership. Never logged,
// stored or put into events.
type Profile struct {
	Verified   bool
	DistrictID *uuid.UUID
	Lang       string
	FirstName  string
	Children   []Child
}

// Candidate is one open request plus the member profile used for scoring.
type Candidate struct {
	Request TeaRequest
	Profile Profile
}

// ScoreWeights are the matching bonuses from config (§ 4.4).
type ScoreWeights struct {
	District int
	Age      int
	Need     int
	Language int
}

// DefaultScoreWeights returns the § 4.4 defaults.
func DefaultScoreWeights() ScoreWeights {
	return ScoreWeights{
		District: DefaultWeightDistrict,
		Age:      DefaultWeightAge,
		Need:     DefaultWeightNeed,
		Language: DefaultWeightLanguage,
	}
}

// Normalize fills zero fields with defaults (config may set only some).
func (w ScoreWeights) Normalize() ScoreWeights {
	d := DefaultScoreWeights()
	if w.District == 0 {
		w.District = d.District
	}
	if w.Age == 0 {
		w.Age = d.Age
	}
	if w.Need == 0 {
		w.Need = d.Need
	}
	if w.Language == 0 {
		w.Language = d.Language
	}
	return w
}

// PairScore is one scored, preference-compatible candidate pair.
type PairScore struct {
	A, B  Candidate
	Score int
}

// MatchCard is a pair card for the viewer: the other mother's first name,
// district, child ages and shared needs — never contacts (§ 4.4).
type MatchCard struct {
	Match
	OtherDisplayName string
	OtherDistrictID  *uuid.UUID
	OtherChildAges   []int
	SharedNeeds      []string
	MyAnswer         *bool
}

// AcquaintanceView is one acquaintance for the API (other member + first name).
type AcquaintanceView struct {
	MemberID    uuid.UUID
	DisplayName string
	Since       time.Time
}

// TeaRequestedData is the payload of circle.TeaRequested v1: the first step
// of the tea funnel metric (ADR-0024 addendum 2026-10-28). It carries only the
// format; preferences and the district stay out of events.
type TeaRequestedData struct {
	RequestID uuid.UUID `json:"request_id"`
	MemberID  uuid.UUID `json:"member_id"`
	Format    string    `json:"format"`
}

// MatchAcceptedData is the payload of circle.MatchAccepted v1.
type MatchAcceptedData struct {
	MatchID uuid.UUID `json:"match_id"`
	MemberA uuid.UUID `json:"member_a"`
	MemberB uuid.UUID `json:"member_b"`
}

// AcquaintanceEstablishedData is the payload of circle.AcquaintanceEstablished v1.
type AcquaintanceEstablishedData struct {
	MemberA uuid.UUID `json:"member_a"`
	MemberB uuid.UUID `json:"member_b"`
}

// DeliveryRecipient is the recipient object of notify.DeliveryRequested v1.
type DeliveryRecipient struct {
	MemberID *uuid.UUID `json:"member_id,omitempty"`
}

// DeliveryRequestedData is the payload of notify.DeliveryRequested v1.
// Template keys and IDs only — never names, districts, ages or needs.
type DeliveryRequestedData struct {
	RequestID uuid.UUID         `json:"request_id"`
	Recipient DeliveryRecipient `json:"recipient"`
	Class     string            `json:"class"`
	Template  string            `json:"template"`
	Params    map[string]string `json:"params,omitempty"`
}

// Event is an outbox event to publish in the current transaction.
type Event struct {
	Type    string
	Version int
	Data    any
}

// deliveryNamespace derives deterministic DeliveryRequested request ids.
var deliveryNamespace = uuid.MustParse("a7e2c91b-4d5e-4f8a-9b1c-6e3d0a8f2c45")

// DeliveryRequestID is deterministic in the subject, template and recipient so
// a retried transaction yields the same id and notify drops the duplicate.
func DeliveryRequestID(subjectID uuid.UUID, template string, memberID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(deliveryNamespace, []byte(subjectID.String()+"|"+template+"|member:"+memberID.String()))
}

// Delivery builds a notify.DeliveryRequested for one member about subjectID.
func Delivery(subjectID, memberID uuid.UUID, template string, params map[string]string) Event {
	mid := memberID
	return Event{
		Type:    EventDeliveryRequested,
		Version: EventVersion,
		Data: DeliveryRequestedData{
			RequestID: DeliveryRequestID(subjectID, template, memberID),
			Recipient: DeliveryRecipient{MemberID: &mid},
			Class:     ClassNormal,
			Template:  template,
			Params:    params,
		},
	}
}
