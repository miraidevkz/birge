// Package service is the analytics application service: the pilot metrics of
// docs/requirements/mvp.md section 9 and their CSV export (section 10), built
// from facts of consumed domain events and the wellbeing cohort aggregates
// (ADR-0024).
//
// Facts. HandleEvent turns one consumed event into facts (model.FactsFromEvent)
// and stores them in one transaction with its platform.processed_events row
// (consumer "analytics.Facts"), so a redelivery stores nothing twice. People
// appear only as pseudonyms (HMAC-SHA256 with Settings.Key); payloads, text
// and districts are never stored. A fact for a member who was deleted is
// dropped by the store (analytics.erased_actors), because her events may
// arrive after the deletion event.
//
// Reports. Metrics and MetricsCSV are staff operations: admin with MFA only,
// deny by default. The period is at most 12 months. Every read and export
// writes its audit entry in the same transaction as the queries, so a report
// that cannot be audited is not served. Every cell built from fewer than k
// members is suppressed by model.Suppress before anything leaves the
// service; k comes from Settings.K.
//
// Retention. The daily analytics.Retention timer rolls facts older than 24
// months (cut at a week boundary) into weekly counts without actors and
// deletes them, together with erased pseudonyms older than the EVENTS
// stream's max age (Settings.EventsMaxAge), after which no late event of a
// deleted member can arrive.
//
// Account deletion. Deletion.DeleteMember computes the member's pseudonym and
// deletes her facts (privacy.DataDeletionRequested, AGENTS.md N6).
//
// Logs carry staff IDs, counts and event names only, never pseudonyms.
package service
