import { afterEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { CheckinHistoryScreen } from './checkin-history';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());

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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000002',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const renderHistory = () =>
  render(
    <Providers>
      <CheckinHistoryScreen />
    </Providers>,
  );

afterEach(() => {
  mockFetch.mockReset();
});

describe('CheckinHistoryScreen', () => {
  it('render: lists a completed check-in point', async () => {
    mockFetch.mockResolvedValue(json([{ completedAt: '2026-10-01T10:00:00Z', phq4: 2, pss4: 5 }]));
    await storage.set('birge.session', 'session-token');
    await renderHistory();
    expect(screen.getByRole('header', { name: 'Past check-ins' })).toBeTruthy();
    expect(await screen.findByText(/PHQ-4 2, PSS-4 5/)).toBeTruthy();
  });

  it('empty: shows the empty state when there are no points', async () => {
    mockFetch.mockResolvedValue(json([]));
    await storage.set('birge.session', 'session-token');
    await renderHistory();
    expect(await screen.findByText('No check-ins yet')).toBeTruthy();
    expect(screen.getByText(/After you complete a weekly check-in/)).toBeTruthy();
  });

  it('error: shows retry when history fails to load', async () => {
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));
    await storage.set('birge.session', 'session-token');
    await renderHistory();
    expect(await screen.findByText('Something went wrong')).toBeTruthy();
    mockFetch.mockResolvedValue(json([]));
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.getByText('No check-ins yet')).toBeTruthy());
  });

  it('main action: retry reloads history after a failure', async () => {
    mockFetch
      .mockRejectedValueOnce(new TypeError('Network request failed'))
      .mockResolvedValueOnce(json([{ completedAt: '2026-10-08T10:00:00Z', phq4: 1, pss4: 3 }]));
    await storage.set('birge.session', 'session-token');
    await renderHistory();
    await fireEvent.press(await screen.findByRole('button', { name: 'Try again' }));
    expect(await screen.findByText(/PHQ-4 1, PSS-4 3/)).toBeTruthy();
  });
});
