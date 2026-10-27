package wiring

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	assistantmodel "birge/internal/domains/assistant/model"
	assistantport "birge/internal/domains/assistant/port"
	"birge/internal/domains/knowledge/retrieval"
	membershipmodel "birge/internal/domains/membership/model"
	"birge/internal/domains/safety/crisis"
)

// LexiconCrisis adapts the safety crisis lexicon to the assistant CrisisDetector
// port (local, pure; no I/O).
type LexiconCrisis struct {
	Lex *crisis.Lexicon
}

var _ assistantport.CrisisDetector = LexiconCrisis{}

// Detect reports whether text matches the level-1 crisis lexicon.
func (l LexiconCrisis) Detect(text, lang string) bool {
	if l.Lex == nil {
		return false
	}
	return l.Lex.Detect(text, lang).Crisis
}

// AssistantProfiles adapts membership.Member to the assistant Profiles port.
type AssistantProfiles struct {
	Members MemberLookup
}

var _ assistantport.Profiles = (*AssistantProfiles)(nil)

// Profile returns the member's display name and language.
func (p *AssistantProfiles) Profile(ctx context.Context, memberID uuid.UUID) (assistantmodel.Profile, error) {
	m, err := p.Members.Member(ctx, memberID)
	if errors.Is(err, membershipmodel.ErrMemberNotFound) {
		return assistantmodel.Profile{}, fmt.Errorf("assistant profiles: %w", err)
	}
	if err != nil {
		return assistantmodel.Profile{}, fmt.Errorf("assistant profiles: %w", err)
	}
	return assistantmodel.Profile{DisplayName: m.DisplayName, Lang: string(m.Lang)}, nil
}

// KnowledgeRetriever adapts retrieval.Hit results to the assistant Retriever
// port (archtest: knowledge/retrieval must not import assistant).
type KnowledgeRetriever struct {
	Inner interface {
		Search(ctx context.Context, lang, scope, text string, k int) ([]retrieval.Hit, error)
	}
}

var _ assistantport.Retriever = (*KnowledgeRetriever)(nil)

// Search embeds and searches; nil Inner behaves like NoRetrieval.
func (r *KnowledgeRetriever) Search(ctx context.Context, lang, scope, text string, k int) ([]assistantmodel.Fragment, error) {
	if r == nil || r.Inner == nil {
		return nil, nil
	}
	hits, err := r.Inner.Search(ctx, lang, scope, text, k)
	if err != nil {
		return nil, err
	}
	out := make([]assistantmodel.Fragment, 0, len(hits))
	for _, h := range hits {
		out = append(out, assistantmodel.Fragment{
			ArticleID: h.ArticleID,
			Title:     h.Title,
			SourceURL: h.SourceURL,
			Text:      h.Text,
		})
	}
	return out, nil
}
