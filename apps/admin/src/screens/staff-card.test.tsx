import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, within } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { StackBack } from '@/lib/stack-back';
import { json, Providers, router as fetchRouter } from '@/test-utils';

import { StaffCardScreen } from './staff-card';

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
  router: {
    push: jest.fn(),
    navigate: jest.fn(),
    back: jest.fn(),
    replace: jest.fn(),
    dismissTo: jest.fn(),
    canGoBack: jest.fn(() => true),
  },
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

type RouterMock = {
  router: { back: jest.Mock; replace: jest.Mock; dismissTo: jest.Mock; canGoBack: jest.Mock };
  useLocalSearchParams: jest.Mock;
};
const expoRouter = () => require('expo-router') as RouterMock;

const STAFF_A = '01900000-0000-7000-8000-0000000000e1';

const CARD = {
  account: {
    id: STAFF_A,
    displayName: 'Example Lawyer',
    roles: ['lawyer'],
    firstSeenAt: '2026-09-01T08:00:00Z',
    lastSeenAt: '2026-10-27T09:00:00Z',
  },
  actions30d: [
    { action: 'article.publish', count: 4 },
    { action: 'future.thing', count: 2 },
  ],
};

const cardRoute = (respond: () => Response) => ({ path: `/staff/accounts/${STAFF_A}`, respond });

const renderScreen = () =>
  render(
    <Providers>
      <StackBack fallback="/audit/staff">
        <StaffCardScreen />
      </StackBack>
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
  expoRouter().useLocalSearchParams.mockReturnValue({ staffId: STAFF_A });
  expoRouter().router.canGoBack.mockReturnValue(true);
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'admin-1', roles: ['admin'] });
});

describe('StaffCardScreen', () => {
  it('shows name, roles, first and last seen and the 30-day action counts', async () => {
    mockFetch.mockImplementation(fetchRouter([cardRoute(() => json(200, CARD))]));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Example Lawyer' })).toBeTruthy();
    const account = within(screen.getByTestId('staff-card-account'));
    expect(account.getByText('Lawyer')).toBeTruthy();
    expect(account.getByText('First seen 2026-09-01 08:00 UTC')).toBeTruthy();
    expect(account.getByText('Last seen 2026-10-27 09:00 UTC')).toBeTruthy();
    const publish = within(screen.getByTestId('staff-action-article.publish'));
    expect(publish.getByText('Published an article')).toBeTruthy();
    expect(publish.getByText('4')).toBeTruthy();
    const unknown = within(screen.getByTestId('staff-action-future.thing'));
    expect(unknown.getByText('Other action')).toBeTruthy();
    expect(unknown.getByText('future.thing')).toBeTruthy();
  });

  it('shows the empty state when there were no actions in 30 days', async () => {
    mockFetch.mockImplementation(fetchRouter([cardRoute(() => json(200, { ...CARD, actions30d: [] }))]));
    await renderScreen();
    expect(await screen.findByText('No actions in 30 days')).toBeTruthy();
  });

  it('says there is no such account on 404 not_found', async () => {
    mockFetch.mockImplementation(fetchRouter([cardRoute(() => json(404, { title: 'not found', code: 'not_found' }))]));
    await renderScreen();
    expect(await screen.findByText('No such staff account')).toBeTruthy();
    expect(screen.queryByText(/not available on this server yet/)).toBeNull();
  });

  it('shows a calm state while the route is not mounted or answers 501', async () => {
    mockFetch.mockImplementation(fetchRouter([cardRoute(() => json(501, { title: 'x', code: 'not_implemented' }))]));
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
  });

  it('shows the error state', async () => {
    mockFetch.mockImplementation(
      fetchRouter([cardRoute(() => json(500, { title: 'server error', code: 'internal' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('goes back to the audit log with the person filter set', async () => {
    mockFetch.mockImplementation(fetchRouter([cardRoute(() => json(200, CARD))]));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId('staff-show-actions'));
    expect(expoRouter().router.dismissTo).toHaveBeenCalledWith({ pathname: '/audit', params: { actorId: STAFF_A } });
  });

  it('has the shared Back, which falls back to the staff list without history', async () => {
    mockFetch.mockImplementation(fetchRouter([cardRoute(() => json(200, CARD))]));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId('screen-back'));
    expect(expoRouter().router.back).toHaveBeenCalled();
    expoRouter().router.canGoBack.mockReturnValue(false);
    await fireEvent.press(screen.getByTestId('screen-back'));
    expect(expoRouter().router.replace).toHaveBeenCalledWith('/audit/staff');
  });
});
