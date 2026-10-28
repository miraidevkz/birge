// Where opening an inbox item leads: one table from template to an in-app
// route. The params come from the server but are still untrusted input: an ID
// is used only when it is a UUID, so it can never change the route's shape.
// A template without a place in this app and an unknown template open
// nothing; the item is still marked read. Without a place today: staff
// templates (they never reach a member).

/** NotificationLike is what the table needs from an inbox item. */
export interface NotificationLike {
  template: string;
  params: Record<string, string>;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** isUuid says whether a value from outside (a param, a route segment) is a UUID. */
export function isUuid(value: unknown): value is string {
  return typeof value === 'string' && UUID.test(value);
}

type Target = (params: Record<string, string>) => string | null;

// byId opens a screen of one object: without a valid id there is nothing to open.
const byId =
  (param: string, route: string): Target =>
  (p) =>
    isUuid(p[param]) ? `${route}/${p[param]}` : null;

/**
 * NOTIFICATION_TARGETS maps a template (internal/domains/<context>/model
 * Template* constants) to its route. Keep it in step with the `templates`
 * keys of the inbox locale namespace.
 */
export const NOTIFICATION_TARGETS: Readonly<Record<string, Target>> = {
  'chat.new_message': (p) => (p.room_id && UUID.test(p.room_id) ? `/chat/${p.room_id}` : '/chat'),
  'circle.match_proposed': () => '/circle',
  'circle.ask_met': () => '/circle',
  // Her own SOS: the SOS screen shows it with the responses.
  'emergency.sos_responded': () => '/sos',
  'emergency.sos_shared': () => '/sos',
  // Another member's SOS, sent to her as a confirmed trusted contact;
  // params {sos_id} (emergency service publishRaised).
  'emergency.sos_raised_contact': byId('sos_id', '/trusted-sos'),
  // Another member invited her to be a trusted contact; params {contact_id}.
  'emergency.trusted_invitation': byId('contact_id', '/trusted-invitation'),
  // A member sent her plan to her as a member contact; params {owner_id}.
  // External contacts get an SMS link instead, never an inbox item.
  'emergency.plan_sent': byId('owner_id', '/shared-plan'),
  'sharing.contact_request': () => '/listings',
  'sharing.listing_decided': () => '/listings',
  'wellbeing.checkin_reminder': () => '/checkin',
};

/** notificationTarget returns the route an inbox item opens, or null when it has none. */
export function notificationTarget(n: NotificationLike): string | null {
  // hasOwn: a template such as "constructor" must not reach Object.prototype.
  if (!Object.hasOwn(NOTIFICATION_TARGETS, n.template)) return null;
  const target = NOTIFICATION_TARGETS[n.template];
  return target ? target(n.params ?? {}) : null;
}

/** isNotificationId checks an ID from a push payload before it is used. */
export function isNotificationId(id: unknown): id is string {
  return isUuid(id);
}
