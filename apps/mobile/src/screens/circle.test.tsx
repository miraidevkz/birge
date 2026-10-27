import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { CircleScreen } from './circle';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn() },
}));

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  const { getSessionToken } = require('@/lib/session');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'android',
      session: getSessionToken,
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000001',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const MATCH_ID = '01900000-0000-7000-8000-0000000000m1';
const TEA_ID = '01900000-0000-7000-8000-0000000000t1';

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function routeFetch(handlers: Record<string, (req: Request) => Response | Promise<Response>>) {
  mockFetch.mockImplementation(async (input, init) => {
    const req = input instanceof Request ? input : new Request(String(input), init);
    const path = new URL(req.url).pathname.replace(/^\/v1/, '');
    const key = `${req.method} ${path}`;
    const handler = handlers[key];
    if (!handler) return json(500, { title: `unhandled ${key}` });
    return handler(req);
  });
}

const renderCircle = () =>
  render(
    <Providers>
      <CircleScreen />
    </Providers>,
  );

beforeEach(async () => {
  mockFetch.mockReset();
  await storage.set('birge.session', 'session-token');
});

describe('CircleScreen', () => {
  it('renders the tea card and match heading', async () => {
    routeFetch({
      'GET /me/tea-requests': () => json(200, []),
      'GET /me/matches': () => json(200, []),
    });
    await renderCircle();
    expect(await screen.findByRole('header', { name: 'Circle' })).toBeTruthy();
    expect(screen.getByText('Tea with someone who understands')).toBeTruthy();
    expect(screen.getByTestId('tea-create')).toBeTruthy();
  });

  it('shows the empty state when there are no matches', async () => {
    routeFetch({
      'GET /me/tea-requests': () => json(200, []),
      'GET /me/matches': () => json(200, []),
    });
    await renderCircle();
    expect(await screen.findByText('No matches yet')).toBeTruthy();
    expect(screen.getByText(/When we find someone/)).toBeTruthy();
  });

  it('shows the error state when matches fail to load', async () => {
    routeFetch({
      'GET /me/tea-requests': () => json(200, []),
      'GET /me/matches': () => json(500, { title: 'server error', code: 'internal' }),
    });
    await renderCircle();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('creates a tea request and accepts a match', async () => {
    let tea: unknown[] = [];
    const match = {
      id: MATCH_ID,
      otherDisplayName: 'Aigul',
      sharedNeeds: ['autism'],
      format: 'call',
      status: 'proposed',
      createdAt: '2026-01-01T00:00:00Z',
    };
    routeFetch({
      'GET /me/tea-requests': () => json(200, tea),
      'GET /me/matches': () => json(200, [match]),
      'POST /me/tea-requests': async (req) => {
        const body = await req.json();
        expect(body).toEqual({
          format: 'call',
          sameDistrict: true,
          similarAge: true,
          sharedNeeds: true,
        });
        expect(req.headers.get('Idempotency-Key')).toBeTruthy();
        const created = {
          id: TEA_ID,
          format: 'call',
          status: 'open',
          createdAt: '2026-01-01T00:00:00Z',
          expiresAt: '2026-01-08T00:00:00Z',
        };
        tea = [created];
        return json(201, created);
      },
      [`POST /me/matches/${MATCH_ID}/answer`]: async (req) => {
        const body = await req.json();
        expect(body).toEqual({ accept: true });
        return json(200, { ...match, status: 'accepted', myAnswer: true });
      },
    });

    await renderCircle();
    expect(await screen.findByText('Aigul')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('tea-create'));
    await fireEvent.press(await screen.findByTestId('tea-submit'));
    await waitFor(() => expect(screen.getByTestId('tea-cancel')).toBeTruthy());

    await fireEvent.press(screen.getByTestId(`match-accept-${MATCH_ID}`));
    await waitFor(() =>
      expect(
        mockFetch.mock.calls.some((c) => {
          const input = c[0];
          const url = input instanceof Request ? input.url : String(input);
          return url.includes(`/me/matches/${MATCH_ID}/answer`);
        }),
      ).toBe(true),
    );
  });
});
