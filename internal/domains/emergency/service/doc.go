// Package service implements the emergency use cases (docs/requirements/mvp.md
// § 4.6): raising, sharing and closing an SOS, the district SOS feed and its
// "I can help" responses, and the emergency plan (child card and up to three
// trusted contacts) with sending it to the confirmed contacts. A confirmed
// member contact reads her owner's SOS and responds to it (GetTrustedSos,
// RespondAsTrustedContact); an invited member reads or declines the
// invitation; moderators and admins work the SOS queue (ListStaffSos,
// MarkSosSeen, CloseSosAsStaff; requirements section 6).
//
// Other contexts import only this package. notify and wiring may rely on:
//
//   - SosAuthor: the member who raised an SOS (for the SosShared district push);
//   - ContactPhone: the phone and SMS language of an external trusted
//     contact, opened at send time; ErrNotFound once the contact is removed;
//   - TrustedContactTarget: whether a trusted_contact_id is a member (and
//     which) or an external contact; ErrNotFound when removed or unconfirmed;
//   - ExternalPlanLink: the one-time link token of an external contact,
//     minted when notify sends the plan SMS; ErrNotFound when there is no
//     live plan send for the contact.
//   - ExternalSosLink: the link token of an external contact for one SOS,
//     minted when notify sends the SOS SMS; ErrNotFound when the SOS is
//     closed, too old or not her owner's (notify then sends the SMS without
//     the link).
//
// # External plan links
//
// An external contact (no account) reads the child card through
// <public URL>/plan#<token> (viewExternalPlan, no session). The token is 32
// random bytes; only its HMAC-SHA256 under Settings.PlanLinkKey is stored.
// A link works for model.PlanLinkTTL after the plan send and is revoked by a
// new send, by removing the contact and by minting a new link; every
// failure is the same ErrNotFound. Every view is audited with the contact
// and owner ids, never the token or the IP. Expired rows are deleted
// model.PlanLinkRetention later by the emergency.PlanLinkPurge timer.
//
// # External SOS links
//
// The SOS SMS of an external contact carries <public URL>/sos-link#<token>
// (viewExternalSos, no session). The token is hashed under the same key with
// the prefix sosLinkHashContext, so a plan token never opens an SOS and the
// reverse. A link ends when the SOS closes or auto-closes and at most
// model.SosLinkMaxTTL after the raise; the single view lookup re-checks the
// SOS and the contact, every failure is ErrNotFound, every view is audited
// before the text is opened, and expired rows are purged
// model.SosLinkRetention later (emergency.SosLinkPurge).
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
// to a confirmed trusted contact after she sent the plan (audited), to a
// confirmed member contact reading her SOS (audited), and to moderators and
// admins with MFA in the SOS queue (every item audited). Events
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
//   - her confirmed member contacts read it and press "I can help" through
//     GetTrustedSos / RespondAsTrustedContact, which admit nobody else
//     (invited, removed, external, blocked or other members get ErrForbidden);
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
// ADR-0019: the feed, responses, responding and the trusted SOS apply Blocks.IsBlocked both
// ways; a blocked member sees nothing and is not told. Block checks fail
// closed: an error from membership is an error, never "not blocked".
package service
