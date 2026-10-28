// Staff-facing date formatting. Times come from the API in UTC (RFC 3339);
// the console shows them in UTC too, so staff in different time zones talk
// about the same moment.

function parse(iso: string): Date | null {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** formatDate returns the UTC calendar date (YYYY-MM-DD) of an RFC 3339 time, or the input when it does not parse. */
export function formatDate(iso: string): string {
  const d = parse(iso);
  return d ? d.toISOString().slice(0, 10) : iso;
}

/** formatDateTime returns "YYYY-MM-DD HH:MM UTC" for an RFC 3339 time, or the input when it does not parse. */
export function formatDateTime(iso: string): string {
  const d = parse(iso);
  return d ? `${d.toISOString().slice(0, 16).replace('T', ' ')} UTC` : iso;
}

const DAY = /^\d{4}-\d{2}-\d{2}$/;

/**
 * isDay reports whether value is a real UTC calendar date written as
 * YYYY-MM-DD (2026-02-30 is not).
 */
export function isDay(value: string): boolean {
  if (!DAY.test(value)) return false;
  const d = parse(`${value}T00:00:00Z`);
  return d !== null && d.toISOString().slice(0, 10) === value;
}

/** dayStart returns the first moment of a YYYY-MM-DD UTC day as RFC 3339 (the caller checks isDay). */
export function dayStart(day: string): string {
  return `${day}T00:00:00Z`;
}

/** dayEnd returns the last second of a YYYY-MM-DD UTC day as RFC 3339, so a range "to" includes that day. */
export function dayEnd(day: string): string {
  return `${day}T23:59:59Z`;
}
