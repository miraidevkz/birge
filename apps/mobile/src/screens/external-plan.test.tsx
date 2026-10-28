import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { Platform } from 'react-native';

import PlanRoute from '@/app/plan';
import { ROOT_ROUTES } from '@/lib/routes';
import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { ExternalPlanScreen } from './external-plan';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());

const mockRedirect = jest.fn<(href: string) => void>();
jest.mock('expo-router', () => ({
  Redirect: ({ href }: { href: string }) => {
    mockRedirect(href);
    return null;
  },
}));

// The real app clients (publicApi included), with fetch replaced before
// lib/api builds them, so the test sees exactly what the page sends.
const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  globalThis.fetch = (...args: Parameters<typeof fetch>) => mockFetch(...args);
  return jest.requireActual('@/lib/api');
});

// Synthetic token of the right shape (43 base64url characters).
const TOKEN = 'Zm9vYmFyYmF6cXV4LXF1dXhfcXV1eHF1dXhxdXV4cXV';
const plan = {
  ownerDisplayName: 'Aigerim',
  childCard: 'Calms down with quiet music. No nuts.',
  sentAt: '2026-10-28T09:30:00Z',
};

function json(data: unknown, status = 200) {
  const type = status >= 400 ? 'application/problem+json' : 'application/json';
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': type } });
}

function problem(status: number) {
  return json({ type: 'about:blank', title: 'problem', status }, status);
}

// A browser window: the location with the link's fragment, and the history
// whose replaceState must remove it.
let replaceState: jest.Mock<History['replaceState']>;
function openLink(hash: string) {
  replaceState = jest.fn<History['replaceState']>();
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { hash, pathname: '/plan', search: '' },
  });
  Object.defineProperty(globalThis, 'history', {
    configurable: true,
    value: { state: { key: 'plan' }, replaceState },
  });
}

// Web storage the page must never write the token to.
function fakeWebStorage() {
  return { getItem: jest.fn(() => null), setItem: jest.fn(), removeItem: jest.fn(), clear: jest.fn() };
}
let local: ReturnType<typeof fakeWebStorage>;
let session: ReturnType<typeof fakeWebStorage>;

function lastRequest(): Request {
  const input = mockFetch.mock.calls.at(-1)?.[0];
  if (!(input instanceof Request)) throw new Error('no request');
  return input;
}

const renderPage = () =>
  render(
    <Providers>
      <ExternalPlanScreen />
    </Providers>,
  );

beforeEach(() => {
  local = fakeWebStorage();
  session = fakeWebStorage();
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: local });
  Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, value: session });
});

afterEach(async () => {
  mockFetch.mockReset();
  mockRedirect.mockReset();
  jest.restoreAllMocks();
  await storage.remove('birge.session');
});

describe('ExternalPlanScreen', () => {
  it('success: shows the neutral title, her name and the child card', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(plan));
    await renderPage();
    expect(screen.getByRole('header', { name: 'Information shared with you' })).toBeTruthy();
    expect(await screen.findByText('Calms down with quiet music. No nuts.')).toBeTruthy();
    expect(screen.getByText(/^Aigerim shared this with you/)).toBeTruthy();
    expect(screen.getByRole('header', { name: 'About the child' })).toBeTruthy();
    // No way into the app: no SOS entry, no Back, no other button.
    expect(screen.queryAllByRole('button')).toHaveLength(0);
  });

  it('request: posts the token in the body to the external view, without a session', async () => {
    // A member is signed in on this browser: her session must not go along.
    await storage.set('birge.session', 'member-session-token');
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(plan));
    await renderPage();
    await screen.findByText('Calms down with quiet music. No nuts.');
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const req = lastRequest();
    expect(req.method).toBe('POST');
    expect(req.url).toBe('https://api.example.invalid/v1/external/plans/view');
    expect(req.headers.get('Authorization')).toBeNull();
    expect(await req.json()).toEqual({ token: TOKEN });
  });

  it('hash: clears the fragment with replaceState and keeps the router state', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(plan));
    await renderPage();
    await screen.findByText('Calms down with quiet music. No nuts.');
    expect(replaceState).toHaveBeenCalledTimes(1);
    expect(replaceState).toHaveBeenCalledWith({ key: 'plan' }, '', '/plan');
  });

  it('storage: never writes or logs the token', async () => {
    const setSpy = jest.spyOn(storage, 'set');
    const logs = (['log', 'info', 'warn', 'error', 'debug'] as const).map((m) =>
      jest.spyOn(console, m).mockImplementation(() => undefined),
    );
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(plan));
    await renderPage();
    await screen.findByText('Calms down with quiet music. No nuts.');
    const written = [...setSpy.mock.calls, ...local.setItem.mock.calls, ...session.setItem.mock.calls];
    expect(JSON.stringify(written)).not.toContain(TOKEN);
    for (const log of logs) expect(JSON.stringify(log.mock.calls)).not.toContain(TOKEN);
  });

  it('404: an expired or unknown link gets the calm expired text', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(problem(404));
    await renderPage();
    expect(await screen.findByText('This link has expired')).toBeTruthy();
    expect(screen.getByText('You can ask her to send the plan again.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
  });

  it.each([
    ['missing', ''],
    ['empty', '#'],
    ['too short', `#${TOKEN.slice(1)}`],
    ['outside base64url', `#${TOKEN.slice(1)}=`],
  ])('malformed hash (%s): shows the expired text without a request', async (_, hash) => {
    openLink(hash);
    await renderPage();
    expect(await screen.findByText('This link has expired')).toBeTruthy();
    expect(mockFetch).not.toHaveBeenCalled();
    // Whatever was there leaves the address bar too.
    if (hash) expect(replaceState).toHaveBeenCalledWith({ key: 'plan' }, '', '/plan');
    else expect(replaceState).not.toHaveBeenCalled();
  });

  it('429: asks to try again a little later', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(problem(429));
    await renderPage();
    expect(await screen.findByText('Let us take a short pause')).toBeTruthy();
    expect(screen.getByText('You can open the link again in a few minutes.')).toBeTruthy();
  });

  it('network error: offers a retry that sends the same token again', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockRejectedValueOnce(new TypeError('Network request failed'));
    await renderPage();
    expect(await screen.findByText('Check your connection and try again.')).toBeTruthy();
    mockFetch.mockResolvedValue(json(plan));
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.getByText('Calms down with quiet music. No nuts.')).toBeTruthy());
    expect(mockFetch).toHaveBeenCalledTimes(2);
    expect(await lastRequest().json()).toEqual({ token: TOKEN });
  });
});

describe('plan route', () => {
  it('web: renders the page signed out, with no sign-in or onboarding redirect', async () => {
    jest.replaceProperty(Platform, 'OS', 'web');
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(plan));
    await render(
      <Providers>
        <PlanRoute />
      </Providers>,
    );
    expect(await screen.findByText('Calms down with quiet music. No nuts.')).toBeTruthy();
    expect(mockRedirect).not.toHaveBeenCalled();
  });

  it('native: goes home instead', async () => {
    jest.replaceProperty(Platform, 'OS', 'android');
    openLink(`#${TOKEN}`);
    await render(
      <Providers>
        <PlanRoute />
      </Providers>,
    );
    expect(mockRedirect).toHaveBeenCalledWith('/');
    expect(mockFetch).not.toHaveBeenCalled();
    expect(replaceState).not.toHaveBeenCalled();
  });

  it('root of the stack: no Back into the app', () => {
    expect(ROOT_ROUTES.has('plan')).toBe(true);
  });
});
