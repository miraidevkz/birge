import { afterEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { Providers } from '@/test-utils';

import { HomeScreen } from './home';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({ router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() } }));

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'android',
      session: async () => 'session-token',
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000003',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

function serve(over: Record<string, unknown> = {}) {
  const data: Record<string, unknown> = {
    '/me/inbox': { items: [] },
    '/me/checkins/schedule': { enabled: false },
    '/me/matches': [],
    '/me/tea-requests': [],
    '/me': { id: '01900000-0000-7000-8000-000000000010', displayName: 'Aigerim', lang: 'en' },
    ...over,
  };
  mockFetch.mockImplementation(async (input) => {
    const path = new URL(String(input instanceof Request ? input.url : input)).pathname.replace('/v1', '');
    return path in data ? json(data[path]) : json({}, 404);
  });
}

const renderHome = () =>
  render(
    <Providers>
      <HomeScreen />
    </Providers>,
  );

afterEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
});

describe('HomeScreen', () => {
  it('greets her by name and shows the cards of the day', async () => {
    serve();
    await renderHome();
    expect(await screen.findByText('Hello, Aigerim')).toBeTruthy();
    expect(screen.getByTestId('home-mood')).toBeTruthy();
    expect(screen.getByTestId('home-checkin')).toBeTruthy();
    expect(await screen.findByText('Mothers who understand are here. Find yours.')).toBeTruthy();
    expect(screen.getByTestId('home-tip')).toBeTruthy();
    expect(await screen.findByText('All quiet for now')).toBeTruthy();
  });

  it('"hard" opens Help and "so-so" starts the check-in; nothing is stored', async () => {
    const { router } = require('expo-router');
    serve();
    await renderHome();
    await fireEvent.press(await screen.findByText('Hard'));
    expect(router.push).toHaveBeenCalledWith('/sos');
    await fireEvent.press(screen.getByText('So-so'));
    expect(router.push).toHaveBeenCalledWith('/checkin');
    const writes = mockFetch.mock.calls.filter(([i]) => i instanceof Request && i.method !== 'GET');
    expect(writes).toHaveLength(0);
  });

  it('shows her tea match in the Circle card', async () => {
    serve({
      '/me/matches': [
        {
          id: '01900000-0000-7000-8000-000000000020',
          otherDisplayName: 'Dana',
          sharedNeeds: [],
          format: 'meeting',
          status: 'accepted',
          createdAt: '2026-10-28T10:00:00Z',
        },
      ],
    });
    await renderHome();
    expect(await screen.findByText('Tea with Dana is agreed. The chat is open.')).toBeTruthy();
  });

  it('still shows the screen when a card fails to load', async () => {
    mockFetch.mockImplementation(async () => json({ code: 'unavailable' }, 503));
    await renderHome();
    expect(await screen.findByText('Hello')).toBeTruthy();
    expect(screen.getByTestId('home-mood')).toBeTruthy();
  });
});
