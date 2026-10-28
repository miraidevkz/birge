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
