// Package service implements the directory use cases (docs/requirements/mvp.md
// § 4.5): specialist search with average rating and the three latest reviews,
// suggesting specialists and putting reviews (verified members; moderation),
// and applying moderation.Decided for kinds specialist and review.
//
// Other contexts import only this package.
//
// # Atomicity
//
// Suggest, put-review and moderation application write rows and outbox events
// in one transaction (ADR-0007). Moderation is deduplicated via
// platform.processed_events.
//
// # Privacy
//
// Events carry object type and id only. Logs carry IDs and event names only.
// Review cards show first name only (from membership MemberInfo).
package service
