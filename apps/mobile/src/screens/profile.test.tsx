import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { ProfileScreen } from './profile';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
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
    kratos: { signOut: jest.fn(async () => undefined) },
  };
});

const member = {
  id: '01900000-0000-7000-8000-000000000001',
  displayName: 'Example Mom',
  lang: 'en',
  verification: 'new',
  roles: ['mom'],
  children: [],
};

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderProfile = () =>
  render(
    <Providers>
      <ProfileScreen />
    </Providers>,
  );

describe('ProfileScreen', () => {
  beforeEach(async () => {
    mockFetch.mockReset();
    await storage.set('birge.session', 'session-token');
  });

  it('renders the member profile', async () => {
    mockFetch.mockResolvedValue(json(member));
    await renderProfile();
    expect(await screen.findByDisplayValue('Example Mom')).toBeTruthy();
    expect(screen.getByRole('header', { name: 'Profile' })).toBeTruthy();
    expect(screen.getByRole('radio', { name: 'English' })).toBeSelected();
  });

  it('shows the error state when the profile fails to load', async () => {
    mockFetch.mockResolvedValue(json({ code: 'unknown', title: 'error' }, 500));
    await renderProfile();
    expect(await screen.findByText('Something went wrong')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('saves a new display name', async () => {
    mockFetch.mockImplementation(async (input) => {
      const req = input as Request;
      if (req.method === 'GET') return json(member);
      return json({ ...member, displayName: 'Updated Mom' });
    });
    await renderProfile();
    await screen.findByDisplayValue('Example Mom');
    await fireEvent.changeText(screen.getByTestId('profile-display-name'), 'Updated Mom');
    await fireEvent.press(screen.getByTestId('profile-save-name'));
    await waitFor(() => expect(screen.getByText('Saved.')).toBeTruthy());
    const patch = mockFetch.mock.calls.map((c) => c[0] as Request).find((r) => r.method === 'PATCH');
    expect(patch).toBeTruthy();
  });

  it('signs out', async () => {
    const { router } = require('expo-router');
    mockFetch.mockResolvedValue(json(member));
    await renderProfile();
    await screen.findByDisplayValue('Example Mom');
    await fireEvent.press(screen.getByTestId('profile-sign-out'));
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/sign-in'));
    expect(await storage.get('birge.session')).toBeNull();
  });
});
