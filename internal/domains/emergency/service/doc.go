// Package service implements the emergency use cases (docs/requirements/mvp.ru.md
// § 4.6): raising, sharing and closing an SOS, the district SOS feed and its
// "I can help" responses, and the emergency plan (child card and up to three
// trusted contacts) with sending it to the confirmed contacts.
//
// Other contexts import only this package. notify and wiring may rely on:
//
//   - SosAuthor: the member who raised an SOS (for the SosShared district push);
//   - ContactPhone: the phone and SMS language of an external trusted
//     contact, opened at send time; ErrNotFound once the contact is removed;
//   - TrustedContactTarget: whether a trusted_contact_id is a member (and
//     which) or an external contact; ErrNotFound when removed or unconfirmed.
//
// # Atomicity
//
// Raising an SOS inserts it, publishes emergency.SosRaised, one critical
// notify.DeliveryRequested to the moderators, one per confirmed trusted
// contact, and schedules emergency.SosAutoClose, in one transaction (ADR-0007,
// ADR-0020). Delivery request ids are deterministic, so a retried transaction
// cannot alert anyone twice.
//
// # Privacy
//
// The SOS text, the child card and external contacts' names and phones are
// sealed with the owner's data key (ADR-0009) and opened only to show them:
// to the author, to verified members of her district once she shares, and
// to a confirmed trusted contact after she sent the plan (audited). Events
// and notifications carry template keys and IDs only. Logs carry IDs and
// event names only: never texts, names, phones or member profiles.
//
// # Childcare (N5)
//
// AGENTS.md non-negotiable 5: nobody outside the mother's confirmed trusted
// contacts is ever suggested for childcare. It is enforced here and below:
//
//   - a childcare SOS goes only to the moderators and to confirmed trusted
//     contacts (RaiseSos);
//   - it can never be shared (ShareSos returns ErrConflict; the store's
//     update refuses it; the table check sos_requests_childcare_never_shared);
//   - the feed query excludes it and Feed drops it again if one ever appears,
//     so nobody can press "I can help" on it (RespondToSos needs feed visibility);
//   - a member becomes a trusted contact only after both met in person
//     (Acquaintances.Met, checked on invitation and again on acceptance) and
//     she accepted.
//
// # Blocks
//
// ADR-0019: the feed, responses and responding apply Blocks.IsBlocked both
// ways; a blocked member sees nothing and is not told. Block checks fail
// closed: an error from membership is an error, never "not blocked".
package service
