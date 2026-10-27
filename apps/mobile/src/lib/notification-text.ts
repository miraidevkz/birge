// Inbox notifications carry a template key, not text (the server never
// stores rendered messages). The client renders the key from the inbox
// locale namespace and falls back to a generic line for templates this
// version does not know yet.

// Same pattern as Notification.template in the contract. Checked again here
// because the key is used to look up translations: a ':' or other separator
// could otherwise select another namespace.
const TEMPLATE = /^[a-z]+\.[a-z_]+$/;

/** notificationKey returns the inbox-namespace key that renders a template. */
export function notificationKey(template: string, exists: (key: string) => boolean): string {
  const key = `templates.${template}`;
  return TEMPLATE.test(template) && exists(key) ? key : 'fallback';
}
