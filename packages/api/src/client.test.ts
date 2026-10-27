import { describe, expect, it } from '@jest/globals';
import { randomBytes } from 'node:crypto';

import { createApiClient, unwrap } from './client';
import { ApiError, NetworkError, isRetryable } from './errors';
import { uuidv7 } from './uuid';

const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function recorder(response: () => Response) {
  const requests: Request[] = [];
  const fetchFn = (async (input: RequestInfo | URL, init?: RequestInit) => {
    requests.push(input instanceof Request ? input : new Request(input, init));
    return response();
  }) as typeof fetch;
  return { requests, fetchFn };
}

function json(status: number, body: unknown, type = 'application/json'): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': type } });
}

function client(fetchFn: typeof fetch, token: string | null = 'session-token') {
  let n = 0;
  return createApiClient({
    baseUrl: 'https://api.example.invalid/v1/',
    appVersion: '1.2.3',
    platform: 'android',
    session: async () => token,
    newIdempotencyKey: () => `00000000-0000-7000-8000-00000000000${n++}`,
    fetch: fetchFn,
  });
}

describe('createApiClient', () => {
  it('sends app headers and the session on every request, and no Idempotency-Key on GET', async () => {
    const r = recorder(() => json(200, { items: [] }));
    await unwrap(client(r.fetchFn).GET('/me/inbox', { params: { query: { limit: 5 } } }));
    const req = r.requests[0]!;
    expect(req.url).toBe('https://api.example.invalid/v1/me/inbox?limit=5');
    expect(req.headers.get('X-App-Version')).toBe('1.2.3');
    expect(req.headers.get('X-App-Platform')).toBe('android');
    expect(req.headers.get('Authorization')).toBe('Bearer session-token');
    expect(req.headers.has('Idempotency-Key')).toBe(false);
  });

  it('omits Authorization when signed out', async () => {
    const r = recorder(() => json(200, { minSupportedVersion: '1.0.0' }));
    await unwrap(client(r.fetchFn, null).GET('/meta/client-config'));
    expect(r.requests[0]!.headers.has('Authorization')).toBe(false);
  });

  it('adds a fresh Idempotency-Key to each mutation', async () => {
    const r = recorder(() => json(202, { escalationId: 'x', crisisPhones: [] }));
    const c = client(r.fetchFn);
    await unwrap(c.POST('/me/help-requests', { body: { source: 'menu' } }));
    await unwrap(c.POST('/me/help-requests', { body: { source: 'menu' } }));
    const keys = r.requests.map((q) => q.headers.get('Idempotency-Key'));
    expect(keys[0]).toBeTruthy();
    expect(keys[0]).not.toBe(keys[1]);
  });

  it('keeps an explicit Idempotency-Key (replay of a stored request)', async () => {
    const r = recorder(() => json(202, { escalationId: 'x', crisisPhones: [] }));
    const key = '01890000-0000-7000-8000-000000000001';
    await unwrap(
      client(r.fetchFn).POST('/me/help-requests', { body: { source: 'menu' }, headers: { 'Idempotency-Key': key } }),
    );
    expect(r.requests[0]!.headers.get('Idempotency-Key')).toBe(key);
  });

  it('maps problem+json to ApiError with code', async () => {
    const r = recorder(() =>
      json(
        429,
        { type: 'about:blank', title: 'Too many requests', status: 429, code: 'rate_limited' },
        'application/problem+json',
      ),
    );
    const err = await unwrap(client(r.fetchFn).POST('/me/help-requests', { body: { source: 'menu' } })).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 429, code: 'rate_limited' });
    expect(isRetryable(err)).toBe(true);
  });

  it('maps an error without a problem body to code "unknown"', async () => {
    const r = recorder(() => new Response('bad gateway', { status: 502 }));
    const err = await unwrap(client(r.fetchFn).GET('/me')).catch((e) => e);
    expect(err).toMatchObject({ status: 502, code: 'unknown' });
  });

  it('turns a failed fetch into NetworkError', async () => {
    const failing = (async () => {
      throw new TypeError('Network request failed');
    }) as typeof fetch;
    const err = await unwrap(client(failing).GET('/me')).catch((e) => e);
    expect(err).toBeInstanceOf(NetworkError);
    expect(isRetryable(err)).toBe(true);
  });

  it('does not retry a client error', () => {
    expect(isRetryable(new ApiError(400, 'invalid_request', 'Bad'))).toBe(false);
    expect(isRetryable(new ApiError(401, 'unauthenticated', 'Sign in'))).toBe(true);
  });
});

describe('uuidv7', () => {
  it('formats version 7 with the timestamp first', () => {
    const a = uuidv7((n) => randomBytes(n), 1_700_000_000_000);
    const b = uuidv7((n) => randomBytes(n), 1_700_000_000_001);
    expect(a).toMatch(UUID_V7);
    expect(a.slice(0, 13).replace('-', '')).toBe((1_700_000_000_000).toString(16).padStart(12, '0'));
    expect(a < b).toBe(true);
  });
});
