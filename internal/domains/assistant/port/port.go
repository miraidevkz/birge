// Package port defines the interfaces the assistant service needs. Other
// contexts (privacy, safety, membership, knowledge) implement them through
// adapters in internal/wiring; this context never imports their internals.
package port

import (
	"context"
	"time"

	"github.com/google/uuid"

	"birge/internal/domains/assistant/model"
)

// ConsentChecker reports the member's current consent (privacy.HasConsent).
type ConsentChecker interface {
	HasConsent(ctx context.Context, memberID uuid.UUID, consentType string) (bool, error)
}

// CrisisDetector is the level-1 crisis lexicon (safety/crisis Detect). It is
// local and pure: no I/O, no network, so it can run before anything else.
type CrisisDetector interface {
	Detect(text, lang string) bool
}

// CrisisOpener opens a human escalation (safety.OpenEscalation, source "assistant").
// It must be idempotent per member while an escalation is open.
type CrisisOpener interface {
	OpenEscalation(ctx context.Context, memberID uuid.UUID, source string) (uuid.UUID, error)
}

// Profiles returns the member's display name and language (membership.Member).
type Profiles interface {
	Profile(ctx context.Context, memberID uuid.UUID) (model.Profile, error)
}

// Retriever searches the knowledge base: top-k published, non-expired
// fragments in lang, limited to the legal part when scope is "rights". The
// knowledge context implements it; text is already anonymized.
type Retriever interface {
	Search(ctx context.Context, lang, scope, text string, k int) ([]model.Fragment, error)
}

// LLM sends one anonymized request to the model and returns its raw JSON
// output. Every error is a provider failure (*model.ProviderError preferred);
// the service never retries and never returns it as a 5xx.
type LLM interface {
	Complete(ctx context.Context, req model.LLMRequest) (model.Completion, error)
}

// Sealer encrypts and opens turn text with the member's data key (ADR-0009).
// Associated data must be assistant.messages.text|<message id>.
type Sealer interface {
	Seal(ctx context.Context, memberID uuid.UUID, plaintext, associatedData []byte) ([]byte, error)
	Open(ctx context.Context, memberID uuid.UUID, ciphertext, associatedData []byte) ([]byte, error)
}

// Store is the persistence port for assistant tables and platform hooks.
type Store interface {
	InTx(ctx context.Context, fn func(tx Tx) error) error
}

// Tx is work inside one database transaction.
type Tx interface {
	// InsertExchange inserts the member turn and the assistant turn. inserted
	// is false (and nothing is written) when the member turn's idempotency key
	// already exists.
	InsertExchange(ctx context.Context, member, assistant model.Turn) (inserted bool, err error)
	// GetExchangeByIdempotencyKey returns the member turn for key and the
	// assistant turn that answers it, or model.ErrNotFound.
	GetExchangeByIdempotencyKey(ctx context.Context, memberID, key uuid.UUID) (member, assistant model.Turn, err error)
	// CountMemberTurnsSince counts the member's own turns created at or after since.
	CountMemberTurnsSince(ctx context.Context, memberID uuid.UUID, since time.Time) (int, error)
	// ListRecentTurns returns the member's newest turns, newest first.
	ListRecentTurns(ctx context.Context, memberID uuid.UUID, limit int) ([]model.Turn, error)

	// GetRatingForUpdate locks the member's assistant turn and returns its
	// current rating, or model.ErrNotFound.
	GetRatingForUpdate(ctx context.Context, memberID, messageID uuid.UUID) (helped *bool, err error)
	SetRating(ctx context.Context, memberID, messageID uuid.UUID, helped bool, at time.Time) error
	PublishReplyRated(ctx context.Context, now time.Time, data model.ReplyRatedData) error
	// PublishQuestionAnswered enqueues assistant.QuestionAnswered in this
	// transaction's outbox. It runs in its own savepoint: on error nothing of
	// it is written and the transaction stays usable, so the crisis path can
	// drop the event and still commit its turn.
	PublishQuestionAnswered(ctx context.Context, now time.Time, data model.QuestionAnsweredData) error

	// PurgeTurnsBefore deletes at most limit turns created before cutoff.
	PurgeTurnsBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
	// ScheduleRetentionPurge schedules the singleton purge timer; scheduled is
	// false when one is already active.
	ScheduleRetentionPurge(ctx context.Context, dueAt time.Time) (scheduled bool, err error)
	// CancelRetentionPurge marks active purge timers done.
	CancelRetentionPurge(ctx context.Context, at time.Time) (int64, error)
}
