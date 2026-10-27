// Package service implements the knowledge use cases: member catalogue and
// article reads, the staff draft→review→publish workflow, expiry timers and
// PublishedFragments for the future retriever (ADR-0022, ADR-0010).
package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/knowledge/model"
	"birge/internal/domains/knowledge/port"
	"birge/internal/platform/auth"
	"birge/internal/platform/clock"
)

const staffPageSize = 50

// Settings is the knowledge runtime configuration (review periods).
type Settings struct {
	// RightsTTL is how long a rights version stays current after review
	// (default ≈ 6 months, ADR-0022).
	RightsTTL time.Duration
	// SelfhelpTTL is how long a self-help version stays current after review
	// (default ≈ 12 months, ADR-0022).
	SelfhelpTTL time.Duration
}

// Service is the knowledge application service.
type Service struct {
	Store    port.Store
	Clock    clock.Clock
	Logger   *slog.Logger
	Settings Settings
}

func (s *Service) log() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) now() time.Time { return s.Clock.Now().UTC() }

func (s *Service) ttlFor(kind string) time.Duration {
	switch kind {
	case model.KindRights:
		if s.Settings.RightsTTL > 0 {
			return s.Settings.RightsTTL
		}
		return model.DefaultRightsTTL
	case model.KindSelfhelp:
		if s.Settings.SelfhelpTTL > 0 {
			return s.Settings.SelfhelpTTL
		}
		return model.DefaultSelfhelpTTL
	}
	return model.DefaultSelfhelpTTL
}

// Actor is an authorized staff member: the principal's id and the role that
// allowed the operation (recorded in the audit log).
type Actor struct {
	ID   uuid.UUID
	Role string
}

// authorize returns the actor when p is a staff principal holding one of
// allowed (deny by default). The first allowed role the principal holds is
// the audited role.
func authorize(p auth.Principal, allowed []string) (Actor, error) {
	if p.IdentityID == uuid.Nil {
		return Actor{}, model.ErrUnauthorized
	}
	if !p.Staff {
		return Actor{}, model.ErrForbidden
	}
	for _, want := range allowed {
		for _, have := range p.Roles {
			if have == want {
				return Actor{ID: p.IdentityID, Role: want}, nil
			}
		}
	}
	return Actor{}, model.ErrForbidden
}

var staffReadRoles = []string{model.RolePsychologist, model.RoleLawyer, model.RoleAdmin}

// memberOf returns the caller's member id. Staff-only and anonymous sessions
// are refused; knowledge catalogue reads are member-facing.
func memberOf(p auth.Principal) (uuid.UUID, error) {
	if p.Staff || p.MemberID == uuid.Nil {
		return uuid.Nil, model.ErrUnauthorized
	}
	return p.MemberID, nil
}

// ListTopics returns topics of kind that have at least one published article
// in lang (member API).
func (s *Service) ListTopics(ctx context.Context, p auth.Principal, kind, lang string) ([]model.Topic, error) {
	if _, err := memberOf(p); err != nil {
		return nil, err
	}
	if !model.KindIsValid(kind) || !model.LangIsValid(lang) {
		return nil, model.ErrInvalidInput
	}
	var out []model.Topic
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.ListMemberTopics(ctx, kind, lang)
		return err
	})
	return out, err
}

// ListTopicArticles returns published article summaries of a topic in lang.
func (s *Service) ListTopicArticles(ctx context.Context, p auth.Principal, topicID uuid.UUID, lang string) ([]model.ArticleSummary, error) {
	if _, err := memberOf(p); err != nil {
		return nil, err
	}
	if topicID == uuid.Nil || !model.LangIsValid(lang) {
		return nil, model.ErrInvalidInput
	}
	now := s.now()
	var out []model.ArticleSummary
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.GetTopic(ctx, topicID); err != nil {
			return err
		}
		var err error
		out, err = tx.ListTopicArticleSummaries(ctx, topicID, lang, now)
		return err
	})
	return out, err
}

// GetArticle returns the published version of an article in lang. Expired
// rights content is marked being_rechecked; self-help stays current until replaced.
func (s *Service) GetArticle(ctx context.Context, p auth.Principal, articleID uuid.UUID, lang string) (model.MemberArticle, error) {
	if _, err := memberOf(p); err != nil {
		return model.MemberArticle{}, err
	}
	if articleID == uuid.Nil || !model.LangIsValid(lang) {
		return model.MemberArticle{}, model.ErrInvalidInput
	}
	now := s.now()
	var out model.MemberArticle
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.GetPublishedArticle(ctx, articleID, lang, now)
		return err
	})
	return out, err
}

// PublishedFragments returns current (non-expired) published versions of kind
// in lang for the future retriever. Callers may rely on Body and SourceURL
// being present only for indexed content (ADR-0010).
func (s *Service) PublishedFragments(ctx context.Context, lang, kind string) ([]model.Fragment, error) {
	if !model.LangIsValid(lang) || !model.KindIsValid(kind) {
		return nil, model.ErrInvalidInput
	}
	now := s.now()
	var out []model.Fragment
	err := s.Store.InTx(ctx, func(tx port.Tx) error {
		var err error
		out, err = tx.ListPublishedFragments(ctx, kind, lang, now)
		return err
	})
	return out, err
}

func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixNano(), 10) + "." + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	ns, idStr, ok := strings.Cut(string(raw), ".")
	if !ok {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	n, err := strconv.ParseInt(ns, 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("%w: cursor", model.ErrInvalidInput)
	}
	return time.Unix(0, n).UTC(), id, nil
}

func auditEntry(at time.Time, a Actor, action, objectType string, objectID uuid.UUID, reason string) model.AuditEntry {
	return model.AuditEntry{
		At: at, ActorID: a.ID, ActorRole: a.Role, Action: action,
		ObjectType: objectType, ObjectID: objectID, Reason: reason,
	}
}
