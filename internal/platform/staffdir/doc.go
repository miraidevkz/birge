// Package staffdir is the staff side of the deployment's own records: the
// staff accounts that have signed in (recorded by the staff auth middleware)
// and read access to the audit log of staff actions (platform.audit_log,
// written by package audit). It is platform, not a bounded context: staff
// identities live at the IdP (ADR-0008 and its staff addendum), and the audit
// log is shared by every context.
//
// # What it owns
//
//   - platform.staff_accounts: one row per staff member whose token reached
//     the API (staff ID, display name from the IdP, mapped roles, first and
//     last seen). PostgresStore.RecordStaffAccount implements
//     staffauth.AccountRecorder; the middleware calls it at most hourly per
//     staff ID per process and never fails a request on its error.
//   - The read side of platform.audit_log for admins.
//
// # HTTP operations (tag staff, generated into package api)
//
// Wiring mounts them under /v1/staff/ behind the staff auth middleware.
//
//   - listAuditEntries: entries whose actor_role is a staff role
//     (psychologist, moderator, lawyer, admin, or the generic staff), plus
//     external_contact (an external trusted contact opening a sent emergency
//     plan by its link: access to a child card admins must see), newest
//     first, 50 per page
//     with an opaque keyset cursor on (occurred_at, id); filters by actor,
//     object type, object ID, action and an inclusive time range. Entries by
//     members and by the system are never listed. Each call writes its own
//     entry (action "audit.read", object type "platform.audit_log", no
//     object ID) in the same transaction as the read, so a read that cannot
//     be audited is not served.
//   - listStaffAccounts: all accounts (up to the contract's 1000), most
//     recently active first.
//   - getStaffAccount: one account and its count per action over the last 30
//     days; 404 with problem code "not_found" for an unknown ID (the admin
//     app reads a 404 without that code as "route not mounted").
//
// All three are admin with MFA only, checked in Service (deny by default:
// other staff roles get 403, a request without a staff principal 401).
//
// # Invariants and retention
//
// Entries carry codes and IDs only (package audit refuses free text); the
// display name is the only personal data here, and it is never logged.
// platform.audit_log is append-only and kept (accountability, ADR-0019 legal
// hold). platform.staff_accounts rows are kept too: they are the names behind
// the staff IDs in the log. The application role has no DELETE on either
// table (db/roles.sql); the IdP stays the authority on who is staff.
package staffdir
