// Package service is the moderation application service: the staff queue of
// listings, specialists, reviews, chat reports, held chat messages and
// verification requests (docs/requirements/mvp.md section 6).
//
// Items are created by consuming the owning contexts' events, idempotently:
// the event ID is recorded in platform.processed_events and an object has at
// most one open item (partial unique index), so a redelivery or a repeated
// submission changes nothing and a repeated report bumps the counter of the
// open report item.
//
// A decision is made by staff with role moderator or admin. It is validated
// against the item kind (model.ValidateDecision), and in one transaction the
// item row is locked and marked decided, the audit entries are written and
// moderation.Decided is put into the outbox; the owning context applies it.
// The row lock makes concurrent decisions on one item serialise: exactly one
// wins, the others get model.ErrAlreadyDecided (409).
//
// Moderation owns no content (ADR-0007, ADR-0019). The queue shows summaries
// fetched from the owners through port.ContentSource at read time; they are
// never stored, logged or published. Logs carry item IDs and kinds only.
package service
