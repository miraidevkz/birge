import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { EscalationsScreen } from './escalations';

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
jest.mock('expo-router', () => ({
  // Keep the screen "focused" for polling tests; the hook is a no-op here.
  useFocusEffect: jest.fn(),
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

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const ESCALATION = {
  id: '01900000-0000-7000-8000-0000000000e1',
  source: 'sos',
  status: 'open',
  openedAt: '2026-10-28T08:00:00Z',
};

const renderScreen = () =>
  render(
    <Providers>
      <EscalationsScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'staff-1', roles: ['psychologist'] });
});

describe('EscalationsScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Escalations' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('No escalations')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('takes an open escalation', async () => {
    mockFetch
      .mockResolvedValueOnce(json(200, { items: [ESCALATION] }))
      .mockResolvedValueOnce(json(200, { ...ESCALATION, status: 'taken', takenByMe: true }))
      .mockResolvedValueOnce(json(200, { items: [{ ...ESCALATION, status: 'taken', takenByMe: true }] }));
    await renderScreen();
    expect(await screen.findByText('SOS')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`take-${ESCALATION.id}`));
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(3));
    const takeCall = mockFetch.mock.calls[1]![0] as Request;
    expect(takeCall.method).toBe('POST');
    expect(takeCall.url).toContain(`/staff/escalations/${ESCALATION.id}/take`);
    expect(takeCall.headers.get('Authorization')).toBe('Bearer staff-token');
  });
});
