// Date formatting shared by screens that show when something was sent.

/** formatDateTime formats an RFC 3339 timestamp as a day, month, year and time in the interface language. */
export function formatDateTime(iso: string, lang: string): string {
  return new Intl.DateTimeFormat(lang, {
    day: 'numeric',
    month: 'long',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(iso));
}
