import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import * as Notifications from 'expo-notifications';

import { DEVICE_KEY, PUSH_KEY, unregisterPush } from '@/lib/push';
import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { ProfileScreen } from './profile';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-notifications', () => require('@/test-mocks').expoNotifications());
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
    <Providers beforeSignOut={unregisterPush}>
      <ProfileScreen />
    </Providers>,
  );

/** serveMember answers every GET with the member and every write with 204. */
function serveMember() {
  mockFetch.mockImplementation(async (input) =>
    (input as Request).method === 'GET' ? json(member) : new Response(null, { status: 204 }),
  );
}

const writes = () => mockFetch.mock.calls.map(([i]) => i as Request).filter((r) => r.method !== 'GET');

describe('ProfileScreen', () => {
  beforeEach(async () => {
    mockFetch.mockReset();
    jest.clearAllMocks();
    await storage.set('birge.session', 'session-token');
    await storage.remove(DEVICE_KEY);
    await storage.remove(PUSH_KEY);
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

  it('asks a moderator for verification when she is not verified', async () => {
    mockFetch.mockImplementation(async (input) => {
      const req = input as Request;
      if (req.method === 'GET') return json({ ...member, verification: 'new' });
      return json({ ...member, verification: 'pending' }, 202);
    });
    await renderProfile();
    await fireEvent.press(await screen.findByTestId('profile-verification-request'));
    expect(await screen.findByText('Request sent. A moderator will call you.')).toBeTruthy();
    const post = mockFetch.mock.calls.map((c) => c[0] as Request).find((r) => r.method === 'POST');
    expect(new URL(post!.url).pathname).toBe('/v1/me/verification-requests');
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

  it('turns push on with the toggle and off again', async () => {
    serveMember();
    await renderProfile();
    const toggle = await screen.findByRole('checkbox', { name: 'Notifications on this phone' });
    await waitFor(() => expect(toggle).toBeEnabled());
    await fireEvent.press(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    expect(writes().map((r) => r.method)).toEqual(['PUT']);
    expect(new URL(writes()[0]!.url).pathname).toMatch(/^\/v1\/me\/devices\//);
    await fireEvent.press(toggle);
    await waitFor(() => expect(toggle).not.toBeChecked());
    expect(writes().map((r) => r.method)).toEqual(['PUT', 'DELETE']);
  });

  it('explains a refused permission and stays off', async () => {
    jest
      .mocked(Notifications.requestPermissionsAsync)
      .mockResolvedValueOnce({ granted: false, canAskAgain: false, status: 'denied' } as never);
    serveMember();
    await renderProfile();
    const toggle = await screen.findByRole('checkbox', { name: 'Notifications on this phone' });
    await waitFor(() => expect(toggle).toBeEnabled());
    await fireEvent.press(toggle);
    expect(await screen.findByText(/turned off in the phone settings/)).toBeTruthy();
    expect(toggle).not.toBeChecked();
    expect(writes()).toHaveLength(0);
  });

  it('stays off without a message when there is no push token', async () => {
    jest.mocked(Notifications.getDevicePushTokenAsync).mockRejectedValueOnce(new Error('no token'));
    serveMember();
    await renderProfile();
    const toggle = await screen.findByRole('checkbox', { name: 'Notifications on this phone' });
    await waitFor(() => expect(toggle).toBeEnabled());
    await fireEvent.press(toggle);
    await waitFor(() => expect(toggle).toBeEnabled());
    expect(toggle).not.toBeChecked();
    expect(screen.queryByText(/phone settings/)).toBeNull();
    expect(writes()).toHaveLength(0);
  });

  it('unregisters the device with the session still valid when she signs out', async () => {
    const { router } = require('expo-router');
    serveMember();
    await renderProfile();
    const toggle = await screen.findByRole('checkbox', { name: 'Notifications on this phone' });
    await waitFor(() => expect(toggle).toBeEnabled());
    await fireEvent.press(toggle);
    await waitFor(() => expect(toggle).toBeChecked());
    await fireEvent.press(screen.getByTestId('profile-sign-out'));
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/sign-in'));
    const del = writes().find((r) => r.method === 'DELETE');
    expect(new URL(del!.url).pathname).toBe(new URL(writes()[0]!.url).pathname);
    expect(del!.headers.get('Authorization')).toBe('Bearer session-token');
    expect(await storage.get(DEVICE_KEY)).toBeNull();
    expect(await storage.get('birge.session')).toBeNull();
  });
});
