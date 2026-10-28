// Detects an endpoint whose contract landed before its server code. Until the
// server is updated such an endpoint answers in one of two ways:
//   - 501 not_implemented: the handler exists as a stub;
//   - 404 without the problem code `not_found`: the route is not mounted at
//     all, so the router answers before any Birge handler.
// A 404 with code `not_found` is a real answer ("this ID does not exist") and
// is not treated as "not available". The screen shows a calm "not available
// on this server yet" state instead of an error, and the query is not retried
// (a retry cannot help until the server is updated).
import { ApiError } from '@birge/api';

/**
 * isNotImplemented reports whether err is the server's answer for a route it
 * does not serve yet: a 501, or a 404 whose problem code is not `not_found`.
 */
export function isNotImplemented(err: unknown): boolean {
  if (!(err instanceof ApiError)) return false;
  if (err.status === 501) return true;
  return err.status === 404 && err.code !== 'not_found';
}

/**
 * isForbidden reports whether err is a role denial: a 403 other than
 * `mfa_required` (the session layer handles that one by asking to sign in again).
 */
export function isForbidden(err: unknown): boolean {
  return err instanceof ApiError && err.status === 403 && err.code !== 'mfa_required';
}
