// Inbox notifications carry a template key, not text (the server never
// stores rendered messages). The client renders the key from the inbox
// locale namespace (`templates.<template>.title` and `.body`) and falls back
// to a generic title and body for templates this version does not know yet,
// so the raw template code is never shown.

// Same pattern as Notification.template in the contract. Checked again here
// because the key is used to look up translations: a ':' or other separator
// could otherwise select another namespace.
const TEMPLATE = /^[a-z]+\.[a-z_]+$/;

/**
 * notificationKey returns the inbox-namespace key prefix that renders a
 * template: append `.title` or `.body`. `exists` checks a full key.
 */
export function notificationKey(template: string, exists: (key: string) => boolean): string {
  const key = `templates.${template}`;
  return TEMPLATE.test(template) && exists(`${key}.title`) && exists(`${key}.body`) ? key : 'fallback';
}
