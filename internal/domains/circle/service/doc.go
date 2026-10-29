// Package service implements the circle use cases (docs/requirements/mvp.md
// § 3 and § 4.4): tea requests, hourly matching, match answers, the
// "did you meet?" confirmation and pairwise acquaintances ("met in person").
//
// Other contexts import only this package. emergency may rely on:
//
//   - Met: whether two members both confirmed they met (symmetric);
//   - ListAcquaintances: the member's confirmed acquaintances.
//
// SetRoomID is meant to be called once chat reports the private room it opened for a mutual
// accept (TODO: wire the MatchAccepted consumer).
//
// # Atomicity
//
// Creating a request, proposing a match, accepting, confirming met and the
// timer handlers write rows, outbox events and timers in one transaction
// (ADR-0007, ADR-0020). Delivery request ids are deterministic so a retried
// transaction cannot notify twice.
//
// # Privacy
//
// Match cards show first name, district, child ages and shared needs to the
// two members only. Events and notifications carry template keys and IDs
// only. Logs carry IDs and event names only: never names, districts, ages
// or needs.
//
// # Blocks
//
// ADR-0019: matching never pairs members blocked either way. Block checks
// fail closed.
package service
