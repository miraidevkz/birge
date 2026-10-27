// Package service implements the sharing use cases (docs/requirements/mvp.md
// § 4.5): item listings with optional photos, the active feed, contact
// requests that open a private chat only with the author's consent, photo
// re-encoding (ADR-0015) and applying moderation.Decided for kind listing.
//
// Other contexts import only this package. Chat will listen to
// sharing.ContactAccepted to open a private room.
//
// # Atomicity
//
// Creating a listing, accepting a contact request, applying moderation and
// the timer handlers write rows, outbox events and timers in one transaction
// (ADR-0007, ADR-0020). Delivery request ids are deterministic so a retried
// transaction cannot notify twice. Moderation application is deduplicated
// via platform.processed_events.
//
// # Privacy
//
// Events and notifications carry template keys and IDs only. Logs carry IDs
// and event names only: never titles, descriptions or photo bytes. Photos
// show only the item (ADR-0019); the server strips EXIF/GPS.
//
// # Blocks
//
// ADR-0019: the feed and contact requests hide members blocked either way.
// Block checks fail closed.
package service
