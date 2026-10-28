// Audit codes the console knows how to name. The server writes short codes
// (platform/audit: `escalation.take`, `safety.escalation`, ...) and never
// prose; the console turns the known ones into locale labels under
// `admin:audit.action.*`, `admin:audit.object.*` and `admin:audit.reason.*`.
// An unknown code is not an error: the row shows a generic label and the raw
// code on a secondary line (codes are not personal data; this is a staff tool).
//
// Locale keys cannot contain the code's dots (i18next reads a dot as nesting),
// so a code maps to its key by replacing dots with underscores.
//
// Keep these lists in step with the codes in internal/ (rg "Audit.*= \"").

/** AUDIT_ACTIONS are the action codes the server writes today, grouped by context. */
export const AUDIT_ACTIONS = [
  'escalation.take',
  'escalation.reveal_contact',
  'escalation.close',
  'duty_shift.add',
  'duty_shift.cancel',
  'moderation.approve',
  'moderation.reject',
  'moderation.block_author',
  'moderation.legal_hold',
  'chat.message.hide',
  'chat.message.restore',
  'chat.message.reports_close',
  'chat.message.legal_hold_on',
  'chat.message.legal_hold_off',
  'chat.member.mute',
  'chat.member.ban',
  'chat.member.unban',
  'chat.room.slow_mode',
  'chat.room.mentor_assign',
  'chat.room.mentor_remove',
  'membership.member.suspend',
  'membership.member.lift_suspension',
  'membership.verification.approve',
  'membership.verification.reject',
  'article.publish',
  'article.reject',
  'topic.create',
  'emergency_plan.view_shared',
  'emergency_plan.view_external',
  'analytics.metrics_read',
  'analytics.metrics_export',
  'audit.read',
] as const;

/** AUDIT_OBJECTS are the object type codes the server writes today. */
export const AUDIT_OBJECTS = [
  'safety.escalation',
  'safety.duty_shift',
  'moderation.item',
  'chat.message',
  'chat.member',
  'chat.room',
  'membership.member',
  'membership.verification_request',
  'knowledge.version',
  'knowledge.topic',
  'emergency.child_card',
  'analytics.metrics',
  'member',
] as const;

/** AUDIT_REASONS are the reason codes of moderation and knowledge rejections. */
export const AUDIT_REASONS = [
  'rules_medicine',
  'rules_money',
  'person_in_photo',
  'unsafe',
  'spam',
  'inaccurate',
  'duplicate',
  'outdated_law',
  'unclear',
  'unsafe_advice',
  'missing_source',
  'other',
] as const;

/**
 * AUDIT_ROLES are the actor roles the console names (`admin:audit.role.*`).
 * The contract's AuditActorRole lists the staff roles and `staff`; the server
 * also writes `external_contact` (an external trusted contact opened a sent
 * emergency plan by its one-time link), which the contract does not list
 * yet. A role outside this list is shown with a generic label and the raw
 * role beneath it, like an unknown code, so a new role never breaks a row.
 */
export const AUDIT_ROLES = ['psychologist', 'moderator', 'lawyer', 'admin', 'staff', 'external_contact'] as const;

const KNOWN_ROLES: ReadonlySet<string> = new Set(AUDIT_ROLES);

/** auditRoleKey returns the `admin` locale key that names an actor role, or undefined for an unknown role. */
export function auditRoleKey(role: string): string | undefined {
  return KNOWN_ROLES.has(role) ? `audit.role.${role}` : undefined;
}

/** AuditCodeKind is which of the three code lists a code belongs to. */
export type AuditCodeKind = 'action' | 'object' | 'reason';

const KNOWN: Record<AuditCodeKind, ReadonlySet<string>> = {
  action: new Set(AUDIT_ACTIONS),
  object: new Set(AUDIT_OBJECTS),
  reason: new Set(AUDIT_REASONS),
};

/**
 * auditLabelKey returns the `admin` locale key that names code, or undefined
 * when the console does not know the code (show the generic label and the
 * code itself then).
 */
export function auditLabelKey(kind: AuditCodeKind, code: string): string | undefined {
  if (!KNOWN[kind].has(code)) return undefined;
  return `audit.${kind}.${code.replaceAll('.', '_')}`;
}

/** shortId returns the first 8 characters of a UUID: enough to tell rows apart on screen. */
export function shortId(id: string): string {
  return id.slice(0, 8);
}
