import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { Platform } from 'react-native';

import SosLinkRoute from '@/app/sos-link';
import { ROOT_ROUTES } from '@/lib/routes';
import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { ExternalSosScreen } from './external-sos';

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
const sos = {
  ownerDisplayName: 'Aigerim',
  type: 'childcare',
  text: 'Need someone to stay with my son for two hours.',
  createdAt: '2026-10-28T09:30:00Z',
  status: 'open',
  closesAt: '2026-10-29T09:30:00Z',
};

function json(data: unknown, status = 200) {
  const type = status >= 400 ? 'application/problem+json' : 'application/json';
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': type } });
}

function problem(status: number) {
  return json({ type: 'about:blank', title: 'problem', status }, status);
}

let replaceState: jest.Mock<History['replaceState']>;
function openLink(hash: string) {
  replaceState = jest.fn<History['replaceState']>();
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { hash, pathname: '/sos-link', search: '' },
  });
  Object.defineProperty(globalThis, 'history', {
    configurable: true,
    value: { state: { key: 'sos-link' }, replaceState },
  });
}

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
      <ExternalSosScreen />
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

describe('ExternalSosScreen', () => {
  it('success: shows her first name, the type, the text, the time and the 112 line', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(sos));
    await renderPage();
    expect(screen.getByRole('header', { name: 'A request for help' })).toBeTruthy();
    expect(await screen.findByText('Need someone to stay with my son for two hours.')).toBeTruthy();
    expect(screen.getByText('Aigerim trusts you and is asking for help.')).toBeTruthy();
    expect(screen.getByRole('header', { name: 'She has no one to leave her child with' })).toBeTruthy();
    expect(screen.getByText('You are her trusted contact: she chose you to help with her child.')).toBeTruthy();
    expect(screen.getByText(/^Sent /)).toBeTruthy();
    expect(screen.getByText(/^Closes automatically /)).toBeTruthy();
    expect(screen.getByText(/get in touch with her the way you usually do/)).toBeTruthy();
    expect(screen.getByText('If there is a threat to life — call 112')).toBeTruthy();
    // No way into the app: the only button dials 112.
    const buttons = screen.queryAllByRole('button');
    expect(buttons).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Call 112' })).toBeTruthy();
  });

  it('request: posts the token in the body to the external view, without a session', async () => {
    await storage.set('birge.session', 'member-session-token');
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(sos));
    await renderPage();
    await screen.findByText('Need someone to stay with my son for two hours.');
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const req = lastRequest();
    expect(req.method).toBe('POST');
    expect(req.url).toBe('https://api.example.invalid/v1/external/sos/view');
    expect(req.headers.get('Authorization')).toBeNull();
    expect(await req.json()).toEqual({ token: TOKEN });
  });

  it('hash: clears the fragment with replaceState and keeps the router state', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(sos));
    await renderPage();
    await screen.findByText('Need someone to stay with my son for two hours.');
    expect(replaceState).toHaveBeenCalledTimes(1);
    expect(replaceState).toHaveBeenCalledWith({ key: 'sos-link' }, '', '/sos-link');
  });

  it('storage: never writes or logs the token', async () => {
    const setSpy = jest.spyOn(storage, 'set');
    const logs = (['log', 'info', 'warn', 'error', 'debug'] as const).map((m) =>
      jest.spyOn(console, m).mockImplementation(() => undefined),
    );
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(sos));
    await renderPage();
    await screen.findByText('Need someone to stay with my son for two hours.');
    const written = [...setSpy.mock.calls, ...local.setItem.mock.calls, ...session.setItem.mock.calls];
    expect(JSON.stringify(written)).not.toContain(TOKEN);
    for (const log of logs) expect(JSON.stringify(log.mock.calls)).not.toContain(TOKEN);
  });

  it('404: a closed request or an expired link gets the calm text and keeps the 112 line', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(problem(404));
    await renderPage();
    expect(await screen.findByText('This request is no longer open')).toBeTruthy();
    expect(
      screen.getByText('She has closed it, or the link has expired. Nothing more is needed from you here.'),
    ).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Call 112' })).toBeTruthy();
  });

  it.each([
    ['missing', ''],
    ['too short', `#${TOKEN.slice(1)}`],
    ['outside base64url', `#${TOKEN.slice(1)}=`],
  ])('malformed hash (%s): shows the calm text without a request', async (_, hash) => {
    openLink(hash);
    await renderPage();
    expect(await screen.findByText('This request is no longer open')).toBeTruthy();
    expect(mockFetch).not.toHaveBeenCalled();
    if (hash) expect(replaceState).toHaveBeenCalledWith({ key: 'sos-link' }, '', '/sos-link');
    else expect(replaceState).not.toHaveBeenCalled();
  });

  it('429: asks to try again a little later', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(problem(429));
    await renderPage();
    expect(await screen.findByText('Let us take a short pause')).toBeTruthy();
  });

  it('network error: the 112 line stays and a retry sends the same token again', async () => {
    openLink(`#${TOKEN}`);
    mockFetch.mockRejectedValueOnce(new TypeError('Network request failed'));
    await renderPage();
    expect(await screen.findByText('Check your connection and try again.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Call 112' })).toBeTruthy();
    mockFetch.mockResolvedValue(json(sos));
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.getByText('Need someone to stay with my son for two hours.')).toBeTruthy());
    expect(mockFetch).toHaveBeenCalledTimes(2);
    expect(await lastRequest().json()).toEqual({ token: TOKEN });
  });
});

describe('sos-link route', () => {
  it('web: renders the page signed out, with no sign-in or onboarding redirect', async () => {
    jest.replaceProperty(Platform, 'OS', 'web');
    openLink(`#${TOKEN}`);
    mockFetch.mockResolvedValue(json(sos));
    await render(
      <Providers>
        <SosLinkRoute />
      </Providers>,
    );
    expect(await screen.findByText('Need someone to stay with my son for two hours.')).toBeTruthy();
    expect(mockRedirect).not.toHaveBeenCalled();
  });

  it('native: goes home instead', async () => {
    jest.replaceProperty(Platform, 'OS', 'android');
    openLink(`#${TOKEN}`);
    await render(
      <Providers>
        <SosLinkRoute />
      </Providers>,
    );
    expect(mockRedirect).toHaveBeenCalledWith('/');
    expect(mockFetch).not.toHaveBeenCalled();
    expect(replaceState).not.toHaveBeenCalled();
  });

  it('root of the stack: no Back into the app', () => {
    expect(ROOT_ROUTES.has('sos-link')).toBe(true);
  });
});
