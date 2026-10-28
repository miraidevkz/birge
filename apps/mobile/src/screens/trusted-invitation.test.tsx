import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

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

function problem(status: number) {
  return new Response(JSON.stringify({ type: 'about:blank', title: 'problem', status }), {
    status,
    headers: { 'Content-Type': 'application/problem+json' },
  });
}

const accepted = () => new Response(null, { status: 204 });

const renderScreen = (contactId = CONTACT) =>
  render(
    <Providers>
      <TrustedInvitationScreen contactId={contactId} />
    </Providers>,
  );

const acceptButton = () => screen.getByRole('button', { name: 'Accept' });

beforeEach(async () => {
  await storage.set('birge.session', 'session-token');
});

afterEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
});

describe('TrustedInvitationScreen', () => {
  it('explains what accepting means and offers accept or not now, without calling the server', async () => {
    await renderScreen();
    expect(screen.getByRole('header', { name: 'Trusted contact invitation' })).toBeTruthy();
    expect(screen.getByText(/invited you to be her trusted contact/)).toBeTruthy();
    expect(screen.getByText(/If she raises an SOS, you will get an alert/)).toBeTruthy();
    expect(acceptButton()).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Not now' })).toBeTruthy();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it('success: accepting posts to the invitation and thanks her', async () => {
    mockFetch.mockResolvedValue(accepted());
    await renderScreen();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText(/You are now her trusted contact/)).toBeTruthy();
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const req = mockFetch.mock.calls[0]?.[0] as Request;
    expect(req.method).toBe('POST');
    expect(req.url).toBe(`https://api.example.invalid/v1/me/trusted-invitations/${CONTACT}/accept`);
    expect(req.headers.get('Idempotency-Key')).toBeTruthy();
    // Done leaves the screen.
    const { router } = require('expo-router');
    await fireEvent.press(screen.getByRole('button', { name: 'Done' }));
    expect(router.back).toHaveBeenCalledTimes(1);
  });

  it('loading: the accept button is busy and not now waits while the request runs', async () => {
    mockFetch.mockReturnValue(new Promise<Response>(() => undefined));
    await renderScreen();
    await fireEvent.press(acceptButton());
    expect(acceptButton()).toBeBusy();
    expect(screen.getByRole('button', { name: 'Not now' })).toBeDisabled();
  });

  it('not now only leaves the screen', async () => {
    const { router } = require('expo-router');
    await renderScreen();
    await fireEvent.press(screen.getByRole('button', { name: 'Not now' }));
    expect(router.back).toHaveBeenCalledTimes(1);
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it('404: a removed invitation says it is no longer active, with nothing to press', async () => {
    mockFetch.mockResolvedValue(problem(404));
    await renderScreen();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText('This invitation is no longer active')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('403: without a confirmed meeting it explains the condition', async () => {
    mockFetch.mockResolvedValue(problem(403));
    await renderScreen();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText('First, a meeting in person')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
  });

  it('error: a failure keeps the invitation and lets her try again', async () => {
    mockFetch.mockResolvedValueOnce(problem(503)).mockResolvedValueOnce(accepted());
    await renderScreen();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText(/Could not accept the invitation/)).toBeTruthy();
    await fireEvent.press(acceptButton());
    expect(await screen.findByText(/You are now her trusted contact/)).toBeTruthy();
    expect(mockFetch).toHaveBeenCalledTimes(2);
  });

  it('a malformed id never reaches the server', async () => {
    await renderScreen('not-a-uuid');
    expect(screen.getByText('This invitation is no longer active')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Accept' })).toBeNull();
    expect(mockFetch).not.toHaveBeenCalled();
  });
});
