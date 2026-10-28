import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { SharedPlanScreen } from './shared-plan';

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

const OWNER = '01900000-0000-7000-8000-0000000000a1';
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

const renderScreen = (ownerId = OWNER) =>
  render(
    <Providers>
      <SharedPlanScreen ownerId={ownerId} />
    </Providers>,
  );

beforeEach(async () => {
  await storage.set('birge.session', 'session-token');
});

afterEach(() => {
  mockFetch.mockReset();
});

describe('SharedPlanScreen', () => {
  it('success: shows her name, the child card and when it was sent, like the external page', async () => {
    mockFetch.mockResolvedValue(json(plan));
    await renderScreen();
    expect(screen.getByRole('header', { name: 'Plan shared with you' })).toBeTruthy();
    expect(await screen.findByText('Calms down with quiet music. No nuts.')).toBeTruthy();
    expect(screen.getByText(/^Aigerim shared this with you/)).toBeTruthy();
    expect(screen.getByRole('header', { name: 'About the child' })).toBeTruthy();
    expect(screen.getByText(/^Sent /)).toBeTruthy();
  });

  it('request: reads the plan of that owner with her session', async () => {
    mockFetch.mockResolvedValue(json(plan));
    await renderScreen();
    await screen.findByText('Calms down with quiet music. No nuts.');
    expect(mockFetch).toHaveBeenCalledTimes(1);
    const req = mockFetch.mock.calls[0]?.[0] as Request;
    expect(req.method).toBe('GET');
    expect(req.url).toBe(`https://api.example.invalid/v1/trusted/plans/${OWNER}`);
    expect(req.headers.get('Authorization')).toBe('Bearer session-token');
  });

  it('loading: shows the loading state until the plan arrives', async () => {
    mockFetch.mockReturnValue(new Promise<Response>(() => undefined));
    await renderScreen();
    expect(await screen.findByText('Loading…')).toBeTruthy();
    expect(screen.queryByText('About the child')).toBeNull();
  });

  it('403: a plan she may not read says only that it is not available', async () => {
    mockFetch.mockResolvedValue(problem(403));
    await renderScreen();
    expect(await screen.findByText('This plan is not available now')).toBeTruthy();
    expect(screen.getByText('If you need it, you can ask her to send the plan again.')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
  });

  it('404: an unknown owner looks the same as a refused one', async () => {
    mockFetch.mockResolvedValue(problem(404));
    await renderScreen();
    expect(await screen.findByText('This plan is not available now')).toBeTruthy();
  });

  it('a malformed id never reaches the server', async () => {
    await renderScreen('../../me/sos');
    expect(screen.getByText('This plan is not available now')).toBeTruthy();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it('error: a server failure offers a retry that loads the plan', async () => {
    mockFetch.mockResolvedValueOnce(problem(500)).mockResolvedValueOnce(json(plan));
    await renderScreen();
    expect(await screen.findByText('Could not load the plan. Please try again in a moment.')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Calms down with quiet music. No nuts.')).toBeTruthy();
  });
});
