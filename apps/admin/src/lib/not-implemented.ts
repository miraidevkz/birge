// Detects an endpoint whose contract landed before its server code. Such an
// endpoint answers 501 not_implemented; the screen shows a calm "not
// available on this server yet" state instead of an error, and the query is
// not retried (a retry cannot help until the server is updated).
import { ApiError } from '@birge/api';

/** isNotImplemented reports whether err is the server's 501 answer for a route it does not serve yet. */
export function isNotImplemented(err: unknown): boolean {
  return err instanceof ApiError && err.status === 501;
}
