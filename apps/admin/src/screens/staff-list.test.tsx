import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { json, Providers, router as fetchRouter } from '@/test-utils';

import { StaffListScreen } from './staff-list';

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
  router: { push: jest.fn(), navigate: jest.fn(), back: jest.fn() },
  useLocalSearchParams: jest.fn(() => ({})),
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

const STAFF_A = '01900000-0000-7000-8000-0000000000e1';
const STAFF_B = '01900000-0000-7000-8000-0000000000e2';

const ACCOUNTS = {
  items: [
    {
      id: STAFF_A,
      displayName: 'Example Moderator',
      roles: ['moderator', 'admin'],
      firstSeenAt: '2026-09-01T08:00:00Z',
      lastSeenAt: '2026-10-27T09:00:00Z',
    },
    { id: STAFF_B, roles: [], firstSeenAt: '2026-10-01T08:00:00Z', lastSeenAt: '2026-10-02T10:30:00Z' },
  ],
};

const accountsRoute = (respond: () => Response) => ({ path: '/staff/accounts', respond });

const renderScreen = () =>
  render(
    <Providers>
      <StaffListScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'admin-1', roles: ['admin'] });
});

describe('StaffListScreen', () => {
  it('lists staff with roles and last seen, a short ID when there is no name', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute(() => json(200, ACCOUNTS))]));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Staff' })).toBeTruthy();
    expect(await screen.findByText('Example Moderator')).toBeTruthy();
    expect(screen.getByText('Moderator, Admin · Last seen 2026-10-27 09:00 UTC')).toBeTruthy();
    expect(screen.getByText(STAFF_B.slice(0, 8))).toBeTruthy();
    expect(screen.getByText('Last seen 2026-10-02 10:30 UTC')).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute(() => json(200, { items: [] }))]));
    await renderScreen();
    expect(await screen.findByText('No staff accounts yet')).toBeTruthy();
  });

  it('shows a calm state while the route is not mounted', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute(() => json(404, { title: 'Not Found' }))]));
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
  });

  it('says only an admin can see it on 403', async () => {
    mockFetch.mockImplementation(
      fetchRouter([accountsRoute(() => json(403, { title: 'forbidden', code: 'forbidden' }))]),
    );
    await renderScreen();
    expect(await screen.findByText('Only an admin can see this')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockImplementation(
      fetchRouter([accountsRoute(() => json(500, { title: 'server error', code: 'internal' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('opens the staff card', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute(() => json(200, ACCOUNTS))]));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`staff-${STAFF_A}`));
    const { router } = require('expo-router') as { router: { push: jest.Mock } };
    expect(router.push).toHaveBeenCalledWith({ pathname: '/audit/staff/[staffId]', params: { staffId: STAFF_A } });
  });
});
