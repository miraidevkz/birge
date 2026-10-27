import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { ModerationScreen } from './moderation';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-web-browser', () => ({
  maybeCompleteAuthSession: jest.fn(),
}));
jest.mock('expo-auth-session', () => ({
  AuthRequest: jest.fn(),
  ResponseType: { Code: 'code' },
  exchangeCodeAsync: jest.fn(),
  fetchDiscoveryAsync: jest.fn(),
  makeRedirectUri: jest.fn(() => 'https://admin.example.invalid/'),
}));

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  const { getAccessToken } = require('@/lib/auth');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'web',
      session: getAccessToken,
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000004',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
  };
});

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const ITEM = {
  id: '01900000-0000-7000-8000-0000000000m1',
  kind: 'listing',
  objectId: '01900000-0000-7000-8000-0000000000o1',
  createdAt: '2026-10-28T08:00:00Z',
  summary: 'Example listing summary',
};

const renderScreen = () =>
  render(
    <Providers>
      <ModerationScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'mod-1', roles: ['moderator'] });
});

describe('ModerationScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Moderation' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('Queue is empty')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('approves a queue item', async () => {
    mockFetch
      .mockResolvedValueOnce(json(200, { items: [ITEM] }))
      .mockResolvedValueOnce(json(204))
      .mockResolvedValueOnce(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('Example listing summary')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`approve-${ITEM.id}`));
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(3));
    const decideCall = mockFetch.mock.calls[1]![0] as Request;
    expect(decideCall.method).toBe('POST');
    expect(decideCall.url).toContain(`/staff/moderation/items/${ITEM.id}/decision`);
  });
});
