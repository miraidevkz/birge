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

/**
 * formatWhen formats a timestamp for a list row: the time for today, the day
 * and month for this year, and the full date otherwise. `now` is passed in so
 * rendering stays pure and testable.
 */
export function formatWhen(iso: string, lang: string, now: Date): string {
  const d = new Date(iso);
  const sameYear = d.getFullYear() === now.getFullYear();
  const today = sameYear && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  const options: Intl.DateTimeFormatOptions = today
    ? { hour: '2-digit', minute: '2-digit' }
    : { day: 'numeric', month: 'short', ...(sameYear ? {} : { year: 'numeric' }) };
  return new Intl.DateTimeFormat(lang, options).format(d);
}
