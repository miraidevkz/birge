import { describe, expect, it } from '@jest/globals';
import { createHash } from 'node:crypto';

import { createKratosClient, SignInError } from './kratos';
import { solvePow } from './pow';

const sha256Hex = async (s: string) => createHash('sha256').update(s).digest('hex');

function challenge(number: number, maxnumber = 200) {
  const salt = 'abcdef?expires=9999999999';
  return {
    algorithm: 'SHA-256',
    challenge: createHash('sha256')
      .update(salt + number)
      .digest('hex'),
    maxnumber,
    salt,
    signature: 'sig',
  };
}

type Route = (req: { method: string; url: URL; body: Record<string, unknown> | null; headers: Headers }) => {
  status: number;
  body?: unknown;
};

function fakeGateway(route: Route) {
  const seen: { method: string; path: string; body: Record<string, unknown> | null; pow: string | null }[] = [];
  const fetchFn = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input));
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as Record<string, unknown>) : null;
    const headers = new Headers(init?.headers);
    seen.push({ method: init?.method ?? 'GET', path: url.pathname, body, pow: headers.get('X-Pow-Solution') });
    if (url.pathname === '/auth/pow') return new Response(JSON.stringify(challenge(42)), { status: 200 });
    const r = route({ method: init?.method ?? 'GET', url, body, headers });
    return new Response(r.body === undefined ? null : JSON.stringify(r.body), { status: r.status });
  }) as typeof fetch;
  const client = createKratosClient({ baseUrl: 'https://api.example.invalid/auth', sha256Hex, fetch: fetchFn });
  return { client, seen };
}

const PHONE = '+70000000000';

describe('solvePow', () => {
  it('finds the number and encodes the ALTCHA payload', async () => {
    const payload = JSON.parse(atob(await solvePow(challenge(137), sha256Hex, { batch: 16 })));
    expect(payload).toMatchObject({
      algorithm: 'SHA-256',
      number: 137,
      salt: 'abcdef?expires=9999999999',
      signature: 'sig',
    });
  });

  it('refuses unknown algorithms and unsolvable challenges', async () => {
    await expect(solvePow({ ...challenge(1), algorithm: 'MD5' }, sha256Hex)).rejects.toThrow('unsupported');
    await expect(solvePow(challenge(300, 100), sha256Hex)).rejects.toThrow('no solution');
  });
});

describe('kratos sign-in', () => {
  it('logs in an existing member: code sent, then session token', async () => {
    const { client, seen } = fakeGateway(({ method, url, body }) => {
      if (method === 'GET' && url.pathname === '/auth/self-service/login/api')
        return { status: 200, body: { id: 'f1' } };
      if (method === 'POST' && url.pathname === '/auth/self-service/login' && !body?.code) {
        return { status: 400, body: { id: 'f1', state: 'sent_email' } };
      }
      return { status: 200, body: { session_token: 'tok', session: { expires_at: '2030-01-01T00:00:00Z' } } };
    });
    const pending = await client.startSignIn(PHONE);
    expect(pending).toEqual({ flow: 'login', flowId: 'f1', phone: PHONE });
    expect(await client.verifyCode(pending, ' 123456 ')).toEqual({ token: 'tok', expiresAt: '2030-01-01T00:00:00Z' });

    const posts = seen.filter((s) => s.method === 'POST');
    expect(posts.map((p) => p.body)).toEqual([
      { method: 'code', identifier: PHONE },
      { method: 'code', identifier: PHONE, code: '123456' },
    ]);
    // A fresh proof-of-work for every submission.
    expect(posts.every((p) => p.pow)).toBe(true);
    expect(seen.filter((s) => s.path === '/auth/pow')).toHaveLength(2);
  });

  it('registers a new member when Kratos reports no account', async () => {
    const { client, seen } = fakeGateway(({ method, url, body }) => {
      if (method === 'GET') return { status: 200, body: { id: url.pathname.includes('login') ? 'l1' : 'r1' } };
      if (url.pathname === '/auth/self-service/login')
        return { status: 400, body: { ui: { messages: [{ id: 4000035 }] } } };
      if (!body?.code) return { status: 400, body: { state: 'sent_email' } };
      return { status: 200, body: { session_token: 'new' } };
    });
    const pending = await client.startSignIn(PHONE);
    expect(pending.flow).toBe('registration');
    expect(await client.verifyCode(pending, '0000')).toEqual({ token: 'new' });
    expect(seen.filter((s) => s.method === 'POST').map((p) => p.body)).toContainEqual({
      method: 'code',
      traits: { phone: PHONE },
      code: '0000',
    });
  });

  it('rejects malformed input before any request', async () => {
    const { client, seen } = fakeGateway(() => ({ status: 500 }));
    await expect(client.startSignIn('87000000000')).rejects.toMatchObject({ code: 'invalid_phone' });
    await expect(client.verifyCode({ flow: 'login', flowId: 'f', phone: PHONE }, 'abc')).rejects.toMatchObject({
      code: 'invalid_code',
    });
    expect(seen).toHaveLength(0);
  });

  it('maps a wrong code, gateway problems and expired flows to codes', async () => {
    const wrong = fakeGateway(() => ({ status: 400, body: { ui: { messages: [{ id: 4010008, type: 'error' }] } } }));
    await expect(wrong.client.verifyCode({ flow: 'login', flowId: 'f', phone: PHONE }, '1111')).rejects.toMatchObject({
      code: 'invalid_code',
    });

    const limited = fakeGateway(() => ({
      status: 429,
      body: { status: 429, title: 'Too many', code: 'rate_limited' },
    }));
    await expect(limited.client.startSignIn(PHONE)).rejects.toMatchObject({ code: 'rate_limited' });

    const expired = fakeGateway(({ method }) =>
      method === 'GET' ? { status: 200, body: { id: 'f' } } : { status: 410 },
    );
    await expect(expired.client.verifyCode({ flow: 'login', flowId: 'f', phone: PHONE }, '1111')).rejects.toMatchObject(
      {
        code: 'flow_expired',
      },
    );
  });

  it('reports network failures as "network"', async () => {
    const client = createKratosClient({
      baseUrl: 'https://api.example.invalid/auth',
      sha256Hex,
      fetch: (async () => {
        throw new TypeError('offline');
      }) as typeof fetch,
    });
    const err = await client.startSignIn(PHONE).catch((e) => e);
    expect(err).toBeInstanceOf(SignInError);
    expect(err.code).toBe('network');
    // The error never carries the number.
    expect(String(err.message)).not.toContain(PHONE);
  });
});
