import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { focusManager, onlineManager, QueryClient } from '@tanstack/react-query';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { trustedSosKey, TrustedSosScreen } from './trusted-sos';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn(), canGoBack: () => true },
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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000000',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
  };
});

const SOS = '01900000-0000-7000-8000-0000000000d1';
const SOS_URL = `https://api.example.invalid/v1/trusted/sos/${SOS}`;
const TEXT = 'Need someone to sit with my son for two hours.';
const open = {
  id: SOS,
  ownerDisplayName: 'Aigerim',
  type: 'childcare',
  text: TEXT,
  status: 'open',
  createdAt: '2026-10-29T09:30:00Z',
  closesAt: '2026-10-30T09:30:00Z',
  respondedByMe: false,
};

function json(data: unknown, status = 200) {
  const type = status >= 400 ? 'application/problem+json' : 'application/json';
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': type } });
}

function problem(status: number) {
  return json({ type: 'about:blank', title: 'problem', status }, status);
}

const done = () => new Response(null, { status: 204 });

type Answer = () => Response | Promise<Response>;

/** serve answers the SOS read and each "I can help" in order. */
function serve(get: Answer, respond: Answer[] = []) {
  const queue = [...respond];
  mockFetch.mockImplementation(async (input) => {
    const req = input as Request;
    if (req.method === 'GET') return get();
    const next = queue.shift();
    if (!next) throw new Error(`unexpected ${req.method} ${req.url}`);
    return next();
  });
}

const calls = () => mockFetch.mock.calls.map((c) => c[0] as Request);
const reads = () => calls().filter((r) => r.method === 'GET');
const helpButton = () => screen.getByRole('button', { name: 'I can help' });

const renderScreen = (sosId = SOS, client?: QueryClient) =>
  render(
    <Providers client={client}>
      <TrustedSosScreen sosId={sosId} />
    </Providers>,
  );

beforeEach(async () => {
  await storage.set('birge.session', 'session-token');
});

afterEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
});

describe('TrustedSosScreen', () => {
  it('open childcare SOS: shows her first name, what she needs, the text, time, status and auto-close', async () => {
    serve(() => json(open));
    await renderScreen();
    expect(screen.getByRole('header', { name: 'A request for help' })).toBeTruthy();
    expect(await screen.findByText(TEXT)).toBeTruthy();
    expect(screen.getByText('Aigerim trusts you and is asking for help.')).toBeTruthy();
    expect(screen.getByRole('header', { name: 'She has no one to leave her child with' })).toBeTruthy();
    expect(screen.getByText(/she chose you to help with her child/)).toBeTruthy();
    expect(screen.getByText(/^Sent /)).toBeTruthy();
    expect(screen.getByText('The request is still open')).toBeTruthy();
    expect(screen.getByText(/^Closes automatically /)).toBeTruthy();
    expect(screen.getByText(/She can then open a chat with you/)).toBeTruthy();
    expect(helpButton()).toBeTruthy();
    // No phone number is shown or asked for.
    expect(screen.queryByText(/phone|\+7/i)).toBeNull();
  });

  it('always shows the call 112 line, as on her own SOS screen, also while loading', async () => {
    mockFetch.mockReturnValue(new Promise<Response>(() => undefined));
    await renderScreen();
    expect(screen.getByText(/threat to life — call 112/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Call 112' })).toBeTruthy();
    expect(await screen.findByText('Loading…')).toBeTruthy();
  });

  it('request: reads that SOS with her session', async () => {
    serve(() => json(open));
    await renderScreen();
    await screen.findByText(TEXT);
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const req = calls()[0] as Request;
    expect(req.method).toBe('GET');
    expect(req.url).toBe(SOS_URL);
    expect(req.headers.get('Authorization')).toBe('Bearer session-token');
  });

  it('other types are named plainly', async () => {
    serve(() => json({ ...open, type: 'item_or_medicine' }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'She urgently needs an item or medicine' })).toBeTruthy();
    expect(screen.queryByText(/help with her child/)).toBeNull();
  });

  it('I can help: answers once, says she has been told, and does not read the SOS again', async () => {
    serve(() => json(open), [done]);
    await renderScreen();
    await screen.findByText(TEXT);
    await fireEvent.press(helpButton());
    expect(await screen.findByRole('header', { name: 'You offered help' })).toBeTruthy();
    expect(screen.getByText(/She has been told. She may open a chat with you/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'I can help' })).toBeNull();
    const posts = calls().filter((r) => r.method === 'POST');
    expect(posts).toHaveLength(1);
    expect((posts[0] as Request).url).toBe(`${SOS_URL}/respond`);
    // Every read is audited: the answer is kept locally, not re-read.
    expect(reads()).toHaveLength(1);
  });

  it('loading: the button is busy while the answer is sent', async () => {
    serve(() => json(open), [() => new Promise<Response>(() => undefined)]);
    await renderScreen();
    await screen.findByText(TEXT);
    await fireEvent.press(helpButton());
    await waitFor(() => expect(helpButton()).toBeBusy());
  });

  it('respondedByMe: shows that she already offered help, with no button', async () => {
    serve(() => json({ ...open, respondedByMe: true }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'You offered help' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'I can help' })).toBeNull();
  });

  it('closed: a calm text, no button and no auto-close time', async () => {
    serve(() => json({ ...open, status: 'closed' }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'This request is closed' })).toBeTruthy();
    expect(screen.getByText(/Nothing more is needed from you now/)).toBeTruthy();
    expect(screen.getByText('The request is closed')).toBeTruthy();
    expect(screen.queryByText(/^Closes automatically/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'I can help' })).toBeNull();
  });

  it('409 on respond: an SOS closed meanwhile gets the same calm text', async () => {
    serve(() => json(open), [() => problem(409)]);
    await renderScreen();
    await screen.findByText(TEXT);
    await fireEvent.press(helpButton());
    expect(await screen.findByRole('header', { name: 'This request is closed' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'I can help' })).toBeNull();
    expect(screen.queryByText(/Could not send your answer/)).toBeNull();
  });

  it.each([403, 404])('%i on read: says only that the request is not available now', async (status) => {
    serve(() => problem(status));
    await renderScreen();
    expect(await screen.findByText('This request is not available now')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
    expect(screen.getByText(/threat to life — call 112/)).toBeTruthy();
  });

  it('403 on respond: access ended meanwhile, the request is not available now', async () => {
    serve(() => json(open), [() => problem(403)]);
    await renderScreen();
    await screen.findByText(TEXT);
    await fireEvent.press(helpButton());
    expect(await screen.findByText('This request is not available now')).toBeTruthy();
    expect(screen.queryByText(TEXT)).toBeNull();
  });

  it('network error on read: offers a retry that loads the SOS', async () => {
    mockFetch.mockRejectedValueOnce(new TypeError('Network request failed'));
    await renderScreen();
    expect(await screen.findByText('Could not load the request. Please try again in a moment.')).toBeTruthy();
    serve(() => json(open));
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText(TEXT)).toBeTruthy();
  });

  it('error on respond: keeps the SOS and lets her try again', async () => {
    serve(() => json(open), [() => problem(503), done]);
    await renderScreen();
    await screen.findByText(TEXT);
    await fireEvent.press(helpButton());
    expect(await screen.findByText(/Could not send your answer/)).toBeTruthy();
    await fireEvent.press(helpButton());
    expect(await screen.findByRole('header', { name: 'You offered help' })).toBeTruthy();
  });

  it('no caching: the text is not refetched on focus or reconnect and leaves the cache with the screen', async () => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    serve(() => json(open));
    const view = await renderScreen(SOS, client);
    await screen.findByText(TEXT);
    const query = client.getQueryCache().find({ queryKey: trustedSosKey(SOS) });
    expect(query?.options.gcTime).toBe(0);
    await act(async () => {
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      onlineManager.setOnline(false);
      onlineManager.setOnline(true);
    });
    expect(reads()).toHaveLength(1);
    await view.unmount();
    await waitFor(() => expect(client.getQueryCache().find({ queryKey: trustedSosKey(SOS) })).toBeUndefined());
    focusManager.setFocused(undefined);
  });

  it('a malformed id never reaches the server', async () => {
    await renderScreen('../../me/sos');
    expect(screen.getByText('This request is not available now')).toBeTruthy();
    expect(mockFetch).not.toHaveBeenCalled();
  });
});
