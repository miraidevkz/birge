import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { TrustedInvitationScreen } from './trusted-invitation';

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

const CONTACT = '01900000-0000-7000-8000-0000000000c1';
const BASE = `https://api.example.invalid/v1/me/trusted-invitations/${CONTACT}`;
const invited = {
  contactId: CONTACT,
  ownerDisplayName: 'Aigerim',
  status: 'invited',
  invitedAt: '2026-10-27T09:30:00Z',
};
const confirmed = { ...invited, status: 'confirmed' };

function json(data: unknown, status = 200) {
  const type = status >= 400 ? 'application/problem+json' : 'application/json';
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': type } });
}

function problem(status: number) {
  return json({ type: 'about:blank', title: 'problem', status }, status);
}

const done = () => new Response(null, { status: 204 });

type Answer = () => Response | Promise<Response>;

/**
 * serve answers the GET of the invitation and each POST by its last path
 * segment (accept, decline); a list of answers is used in order.
 */
function serve(routes: { get?: Answer; accept?: Answer[]; decline?: Answer[] }) {
  const accept = [...(routes.accept ?? [])];
  const decline = [...(routes.decline ?? [])];
  mockFetch.mockImplementation(async (input) => {
    const req = input as Request;
    if (req.method === 'GET') return (routes.get ?? (() => json(invited)))();
    const next = req.url.endsWith('/accept') ? accept.shift() : decline.shift();
    if (!next) throw new Error(`unexpected ${req.method} ${req.url}`);
    return next();
  });
}

const posts = () => mockFetch.mock.calls.map((c) => c[0] as Request).filter((r) => r.method === 'POST');

const renderScreen = (contactId = CONTACT) =>
  render(
    <Providers>
      <TrustedInvitationScreen contactId={contactId} />
    </Providers>,
  );

const acceptButton = () => screen.getByRole('button', { name: 'Accept' });
const loaded = () => screen.findByText(/Aigerim, whom you have met in person, invited you/);

beforeEach(async () => {
  await storage.set('birge.session', 'session-token');
});

afterEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
});

describe('TrustedInvitationScreen', () => {
  it('invited: names who invited her and offers accept, not now and decline', async () => {
    serve({});
    await renderScreen();
    expect(screen.getByRole('header', { name: 'Trusted contact invitation' })).toBeTruthy();
    expect(await loaded()).toBeTruthy();
    expect(screen.getByText(/If she raises an SOS, you will get an alert/)).toBeTruthy();
    expect(acceptButton()).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Not now' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Decline' })).toBeTruthy();
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const req = mockFetch.mock.calls[0]?.[0] as Request;
    expect(req.method).toBe('GET');
    expect(req.url).toBe(BASE);
  });

  it('loading: shows the loading state until the invitation arrives', async () => {
    mockFetch.mockReturnValue(new Promise<Response>(() => undefined));
    await renderScreen();
    expect(await screen.findByText('Loading…')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('404 on read: an invitation that is gone says it is no longer active, with nothing to press', async () => {
    serve({ get: () => problem(404) });
    await renderScreen();
    expect(await screen.findByText('This invitation is no longer active')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Decline' })).toBeNull();
  });

  it('error on read: a network failure offers a retry that loads the invitation', async () => {
    mockFetch.mockRejectedValueOnce(new TypeError('Network request failed'));
    await renderScreen();
    expect(await screen.findByText('Could not load the invitation. Please try again in a moment.')).toBeTruthy();
    serve({});
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(await loaded()).toBeTruthy();
  });

  it('success: accepting posts to the invitation and thanks her', async () => {
    serve({ accept: [done] });
    await renderScreen();
    await loaded();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText(/You are now her trusted contact/)).toBeTruthy();
    expect(posts()).toHaveLength(1);
    const req = posts()[0] as Request;
    expect(req.url).toBe(`${BASE}/accept`);
    expect(req.headers.get('Idempotency-Key')).toBeTruthy();
    // Done leaves the screen.
    const { router } = require('expo-router');
    await fireEvent.press(screen.getByRole('button', { name: 'Done' }));
    expect(router.back).toHaveBeenCalledTimes(1);
  });

  it('loading: the accept button is busy and the other choices wait while the request runs', async () => {
    serve({ accept: [() => new Promise<Response>(() => undefined)] });
    await renderScreen();
    await loaded();
    await fireEvent.press(acceptButton());
    // The mutation's pending state reaches the screen through React Query's
    // batched notify, after the press resolves.
    await waitFor(() => expect(acceptButton()).toBeBusy());
    expect(screen.getByRole('button', { name: 'Not now' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Decline' })).toBeDisabled();
  });

  it('not now only leaves the screen', async () => {
    const { router } = require('expo-router');
    serve({});
    await renderScreen();
    await loaded();
    await fireEvent.press(screen.getByRole('button', { name: 'Not now' }));
    expect(router.back).toHaveBeenCalledTimes(1);
    expect(posts()).toHaveLength(0);
  });

  it('404 on accept: a removed invitation says it is no longer active, with nothing to press', async () => {
    serve({ accept: [() => problem(404)] });
    await renderScreen();
    await loaded();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText('This invitation is no longer active')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('403 on accept: without a confirmed meeting it explains the condition', async () => {
    serve({ accept: [() => problem(403)] });
    await renderScreen();
    await loaded();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText('First, a meeting in person')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('error on accept: a failure keeps the invitation and lets her try again', async () => {
    serve({ accept: [() => problem(503), done] });
    await renderScreen();
    await loaded();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText(/Could not accept the invitation/)).toBeTruthy();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText(/You are now her trusted contact/)).toBeTruthy();
    expect(posts()).toHaveLength(2);
  });

  it('decline: asks once, names what changes, then declines', async () => {
    serve({ decline: [done] });
    await renderScreen();
    await loaded();
    await fireEvent.press(screen.getByRole('button', { name: 'Decline' }));
    // Nothing is sent before she confirms.
    expect(posts()).toHaveLength(0);
    expect(screen.getByText('Decline the invitation?')).toBeTruthy();
    expect(screen.getByText(/You will not get Aigerim's SOS alerts or her plan/)).toBeTruthy();
    await fireEvent.press(screen.getByTestId('trusted-invitation-decline-sheet-confirm'));
    expect(await screen.findByText(/You declined the invitation/)).toBeTruthy();
    expect(posts()).toHaveLength(1);
    expect((posts()[0] as Request).url).toBe(`${BASE}/decline`);
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('decline error: a failure keeps the invitation and says so', async () => {
    serve({ decline: [() => problem(503)] });
    await renderScreen();
    await loaded();
    await fireEvent.press(screen.getByRole('button', { name: 'Decline' }));
    await fireEvent.press(screen.getByTestId('trusted-invitation-decline-sheet-confirm'));
    expect(await screen.findByText(/Could not save your answer/)).toBeTruthy();
    expect(acceptButton()).toBeTruthy();
  });

  it('decline 404: an invitation removed meanwhile is no longer active', async () => {
    serve({ decline: [() => problem(404)] });
    await renderScreen();
    await loaded();
    await fireEvent.press(screen.getByRole('button', { name: 'Decline' }));
    await fireEvent.press(screen.getByTestId('trusted-invitation-decline-sheet-confirm'));
    expect(await screen.findByText('This invitation is no longer active')).toBeTruthy();
  });

  it('confirmed: says she is already a contact and offers to stop, with a confirmation', async () => {
    serve({ get: () => json(confirmed), decline: [done] });
    await renderScreen();
    expect(await screen.findByText("You are already Aigerim's trusted contact.")).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Decline' })).toBeNull();
    await fireEvent.press(screen.getByRole('button', { name: 'Stop being her trusted contact' }));
    expect(posts()).toHaveLength(0);
    expect(screen.getByText('Stop being her trusted contact?')).toBeTruthy();
    expect(screen.getByText(/You will no longer get Aigerim's SOS alerts or her plan/)).toBeTruthy();
    await fireEvent.press(screen.getByTestId('trusted-invitation-decline-sheet-confirm'));
    expect(await screen.findByText('You are no longer her trusted contact.')).toBeTruthy();
    expect((posts()[0] as Request).url).toBe(`${BASE}/decline`);
  });

  it('a malformed id never reaches the server', async () => {
    await renderScreen('not-a-uuid');
    expect(screen.getByText('This invitation is no longer active')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(mockFetch).not.toHaveBeenCalled();
  });
});
