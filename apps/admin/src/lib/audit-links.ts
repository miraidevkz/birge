// Where an audit entry's object can be opened in the console. One place for
// the mapping, so a new screen adds one line here. An object type without a
// screen (a chat message, a verification request, a child card) gets no link.
// The member lists cannot be filtered by member ID (the API has no such
// filter), so a member links to the Members tab as a whole.

/** AuditObjectLink is the tab route that shows an object, and the `admin:tabs.*` key naming that tab. */
export interface AuditObjectLink {
  href: '/' | '/moderation' | '/members' | '/rooms' | '/duty' | '/knowledge';
  tab: 'escalations' | 'moderation' | 'members' | 'rooms' | 'duty' | 'knowledge';
}

const ESCALATIONS: AuditObjectLink = { href: '/', tab: 'escalations' };
const MODERATION: AuditObjectLink = { href: '/moderation', tab: 'moderation' };
const MEMBERS: AuditObjectLink = { href: '/members', tab: 'members' };
const ROOMS: AuditObjectLink = { href: '/rooms', tab: 'rooms' };
const DUTY: AuditObjectLink = { href: '/duty', tab: 'duty' };
const KNOWLEDGE: AuditObjectLink = { href: '/knowledge', tab: 'knowledge' };

const LINKS: Readonly<Record<string, AuditObjectLink>> = {
  'safety.escalation': ESCALATIONS,
  'safety.duty_shift': DUTY,
  'moderation.item': MODERATION,
  'chat.member': MEMBERS,
  'membership.member': MEMBERS,
  'chat.room': ROOMS,
  'knowledge.version': KNOWLEDGE,
  'knowledge.topic': KNOWLEDGE,
};

/** auditObjectLink returns where objects of this type are shown in the console, or undefined. */
export function auditObjectLink(objectType: string): AuditObjectLink | undefined {
  return Object.hasOwn(LINKS, objectType) ? LINKS[objectType] : undefined;
}
