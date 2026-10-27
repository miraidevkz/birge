// Package model holds the knowledge domain types and invariants: topics,
// articles, version workflow (draft → in_review → published → retired),
// events, timers and errors (ADR-0022). No I/O.
package model

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Kind of knowledge content.
const (
	KindRights   = "rights"
	KindSelfhelp = "selfhelp"
)

// Version statuses (lifecycle draft → in_review → published → retired).
const (
	StatusDraft     = "draft"
	StatusInReview  = "in_review"
	StatusPublished = "published"
	StatusRetired   = "retired"
)

// Member-facing availability of a published version.
const (
	AvailabilityCurrent        = "current"
	AvailabilityBeingRechecked = "being_rechecked"
)

// Interface languages at launch.
const (
	LangKK = "kk"
	LangRU = "ru"
	LangEN = "en"
)

// Staff roles that author or review knowledge (auth.Principal role names).
const (
	RolePsychologist = "psychologist"
	RoleLawyer       = "lawyer"
	RoleAdmin        = "admin"
)

// Reject reason codes (contract enum; never free text).
const (
	RejectInaccurate    = "inaccurate"
	RejectOutdatedLaw   = "outdated_law"
	RejectUnclear       = "unclear"
	RejectUnsafeAdvice  = "unsafe_advice"
	RejectMissingSource = "missing_source"
	RejectOther         = "other"
)

// Events published by knowledge.
const (
	EventArticlePublished = "knowledge.ArticlePublished"
	EventArticleExpired   = "knowledge.ArticleExpired"
	EventVersion          = 1
)

// Timer kinds (ADR-0020). Subject is the version id.
const (
	TimerReviewDue = "knowledge.ReviewDue"
	TimerExpire    = "knowledge.Expire"
)

// Audit actions written to platform.audit_log.
const (
	AuditObjectVersion = "knowledge.version"
	AuditObjectTopic   = "knowledge.topic"
	AuditPublish       = "article.publish"
	AuditReject        = "article.reject"
	AuditTopicCreate   = "topic.create"
)

// DisclaimerKeyRights is the locale key carried on every rights answer
// (§ 4.7: does not replace a lawyer's advice).
const DisclaimerKeyRights = "knowledge.lawyer_disclaimer"

// Content length limits (contract).
const (
	MaxTitleRunes = 200
	MaxBodyRunes  = 20000
	MaxCodeLen    = 40
	MaxTopicTitle = 120
	MaxSourceURL  = 2048
)

// Default review periods when Settings leave a duration unset (ADR-0022).
const (
	DefaultRightsTTL   = 6 * 30 * 24 * time.Hour  // ≈ 6 months
	DefaultSelfhelpTTL = 12 * 30 * 24 * time.Hour // ≈ 12 months
	ReviewDueLead      = 30 * 24 * time.Hour      // one month before expiry
)

var (
	// ErrUnauthorized means there is no principal of the required kind.
	ErrUnauthorized = errors.New("knowledge: unauthorized")
	// ErrForbidden means the principal lacks the matching role or is the author on publish.
	ErrForbidden = errors.New("knowledge: forbidden")
	// ErrNotFound means the topic, article or version does not exist.
	ErrNotFound = errors.New("knowledge: not found")
	// ErrInvalidInput means a request failed validation.
	ErrInvalidInput = errors.New("knowledge: invalid input")
	// ErrConflict means the version is not in a state that allows the operation.
	ErrConflict = errors.New("knowledge: conflict")
)

var codeRe = regexp.MustCompile(`^[a-z_]{2,40}$`)

// KindIsValid reports whether k is a known knowledge kind.
func KindIsValid(k string) bool { return k == KindRights || k == KindSelfhelp }

// LangIsValid reports whether l is a supported interface language.
func LangIsValid(l string) bool {
	return l == LangKK || l == LangRU || l == LangEN
}

// StatusIsValid reports whether s is a known version status.
func StatusIsValid(s string) bool {
	switch s {
	case StatusDraft, StatusInReview, StatusPublished, StatusRetired:
		return true
	}
	return false
}

// RejectIsValid reports whether r is a known reject reason code.
func RejectIsValid(r string) bool {
	switch r {
	case RejectInaccurate, RejectOutdatedLaw, RejectUnclear,
		RejectUnsafeAdvice, RejectMissingSource, RejectOther:
		return true
	}
	return false
}

// ReviewerRoleFor returns the staff role that may publish or reject a kind.
func ReviewerRoleFor(kind string) string {
	switch kind {
	case KindRights:
		return RoleLawyer
	case KindSelfhelp:
		return RolePsychologist
	}
	return ""
}

// Topic is a browse category with per-language titles.
type Topic struct {
	ID        uuid.UUID
	Kind      string
	Code      string
	TitleKK   *string
	TitleRU   *string
	TitleEN   *string
	CreatedAt time.Time
}

// TitleIn returns the title for lang, or the code when that language is unset.
func (t Topic) TitleIn(lang string) string {
	var p *string
	switch lang {
	case LangKK:
		p = t.TitleKK
	case LangRU:
		p = t.TitleRU
	case LangEN:
		p = t.TitleEN
	}
	if p != nil && strings.TrimSpace(*p) != "" {
		return *p
	}
	return t.Code
}

// Article is one question under a topic.
type Article struct {
	ID        uuid.UUID
	TopicID   uuid.UUID
	Kind      string
	CreatedAt time.Time
}

// Version is one language version of an article.
type Version struct {
	ID             uuid.UUID
	ArticleID      uuid.UUID
	TopicID        uuid.UUID // filled when joined with the article
	Kind           string    // filled when joined with the article
	Lang           string
	Status         string
	Title          string
	Body           string
	SourceURL      *string
	AuthorID       uuid.UUID
	ReviewerID     *uuid.UUID
	ReviewedAt     *time.Time
	ExpiresAt      *time.Time
	RejectReason   *string
	NeedsSync      bool
	IdempotencyKey *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Content is the editable title, body and optional source URL of a draft.
type Content struct {
	Title     string
	Body      string
	SourceURL *string
}

// MemberArticle is a published answer shown to a member.
type MemberArticle struct {
	ID         uuid.UUID
	TopicID    uuid.UUID
	Kind       string
	Lang       string
	Title      string
	Body       string
	SourceURL  *string
	ReviewedAt time.Time
	ExpiresAt  time.Time
	Status     string // current | being_rechecked
}

// ArticleSummary is a published article title in a topic catalogue.
type ArticleSummary struct {
	ID     uuid.UUID
	Title  string
	Status string
}

// Fragment is a current (non-expired) published version for retrieval.
type Fragment struct {
	ArticleID uuid.UUID
	VersionID uuid.UUID
	Title     string
	SourceURL *string
	Body      string
}

// StaffPage is one page of staff article versions.
type StaffPage struct {
	Items      []Version
	NextCursor string
}

// VersionFilter selects a staff list page, newest first.
type VersionFilter struct {
	Status          string
	CursorUpdatedAt *time.Time
	CursorID        *uuid.UUID
	Limit           int32
}

// AuditEntry is one platform.audit_log row (IDs and fixed codes only).
type AuditEntry struct {
	At         time.Time
	ActorID    uuid.UUID
	ActorRole  string
	Action     string
	ObjectType string
	ObjectID   uuid.UUID
	Reason     string
}

// Event is an outbox event to publish in the current transaction.
type Event struct {
	Type    string
	Version int
	Data    any
}

// ArticlePublishedData is the payload of knowledge.ArticlePublished v1.
type ArticlePublishedData struct {
	ArticleID uuid.UUID `json:"article_id"`
	VersionID uuid.UUID `json:"version_id"`
	Lang      string    `json:"lang"`
	Kind      string    `json:"kind"`
}

// ArticleExpiredData is the payload of knowledge.ArticleExpired v1.
type ArticleExpiredData struct {
	ArticleID uuid.UUID `json:"article_id"`
	VersionID uuid.UUID `json:"version_id"`
	Lang      string    `json:"lang"`
}

// AvailabilityOf returns the member-facing status of a published version at now.
// Rights past expires_at are being_rechecked; self-help stays current until replaced.
func AvailabilityOf(kind string, expiresAt time.Time, now time.Time) string {
	if kind == KindRights && !now.Before(expiresAt) {
		return AvailabilityBeingRechecked
	}
	return AvailabilityCurrent
}

// ValidateCode checks a topic code against the contract pattern.
func ValidateCode(code string) error {
	if !codeRe.MatchString(code) {
		return ErrInvalidInput
	}
	return nil
}

// ValidateContent checks title, body and optional source URL lengths and UTF-8.
func ValidateContent(c Content) error {
	if !utf8.ValidString(c.Title) || strings.TrimSpace(c.Title) == "" || utf8.RuneCountInString(c.Title) > MaxTitleRunes {
		return ErrInvalidInput
	}
	if !utf8.ValidString(c.Body) || strings.TrimSpace(c.Body) == "" || utf8.RuneCountInString(c.Body) > MaxBodyRunes {
		return ErrInvalidInput
	}
	if c.SourceURL != nil {
		u := strings.TrimSpace(*c.SourceURL)
		if u == "" || len(u) > MaxSourceURL {
			return ErrInvalidInput
		}
		parsed, err := url.ParseRequestURI(u)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return ErrInvalidInput
		}
	}
	return nil
}

// ValidateTopicTitle checks an optional topic title.
func ValidateTopicTitle(t *string) error {
	if t == nil {
		return nil
	}
	if !utf8.ValidString(*t) || strings.TrimSpace(*t) == "" || utf8.RuneCountInString(*t) > MaxTopicTitle {
		return ErrInvalidInput
	}
	return nil
}
