// Package staffdir is the staff side of the deployment's own records: the
// staff accounts that have signed in (recorded by the staff auth middleware)
// and read access to the audit log of staff actions (platform.audit_log,
// written by package audit). It is platform, not a bounded context: staff
// identities live at the IdP (ADR-0008), and the audit log is shared by every
// context.
//
// Its HTTP operations (tag staff in the contract) are generated into package
// api. Until they are implemented they are not mounted, so the routes answer
// 404 and the admin app shows them as not available.
package staffdir
