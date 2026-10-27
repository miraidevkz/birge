// Errors the API client throws. Callers branch on the class (offline or not)
// and on `code`, never on human-readable text: problem titles are English and
// for developers; users see texts from the locale files keyed by `code`.

/** ApiError is a non-2xx answer, mapped from RFC 9457 problem details. */
export class ApiError extends Error {
  /** status is the HTTP status code. */
  readonly status: number;
  /** code is the stable machine-readable error code ("unknown" when the body had none). */
  readonly code: string;
  /** detail is the optional problem detail, for diagnostics only; never log it with user data. */
  readonly detail: string | undefined;

  constructor(status: number, code: string, title: string, detail?: string) {
    super(title);
    this.name = 'ApiError';
    this.status = status;
    this.code = code;
    this.detail = detail;
  }
}

/**
 * NetworkError means the request never got an HTTP answer: offline, DNS,
 * TLS, timeout or abort. The request may or may not have reached the server,
 * which is why every mutation carries an Idempotency-Key and can be retried.
 */
export class NetworkError extends Error {
  constructor(cause?: unknown) {
    super('network request failed', { cause });
    this.name = 'NetworkError';
  }
}

/** toApiError builds an ApiError from a status and a parsed response body of any shape. */
export function toApiError(status: number, body: unknown): ApiError {
  if (body !== null && typeof body === 'object') {
    const p = body as Record<string, unknown>;
    const code = typeof p.code === 'string' && p.code !== '' ? p.code : 'unknown';
    const title = typeof p.title === 'string' ? p.title : `HTTP ${status}`;
    const detail = typeof p.detail === 'string' ? p.detail : undefined;
    return new ApiError(status, code, title, detail);
  }
  return new ApiError(status, 'unknown', `HTTP ${status}`);
}

/**
 * isRetryable reports whether repeating the same request later can succeed:
 * no answer at all, a timeout, rate limiting, a server error, or an expired
 * session (the member signs in again and the request is replayed).
 */
export function isRetryable(err: unknown): boolean {
  if (err instanceof NetworkError) return true;
  if (err instanceof ApiError) {
    return err.status === 401 || err.status === 408 || err.status === 425 || err.status === 429 || err.status >= 500;
  }
  return false;
}
