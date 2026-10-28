import { afterEach, beforeEach, describe, expect, it, jest } from '@jest/globals';
import { QueryClient } from '@tanstack/react-query';
import { waitFor } from '@testing-library/react-native';
import { router } from 'expo-router';
import * as Notifications from 'expo-notifications';
import { Platform } from 'react-native';

import { config } from './config';
import { openFromPush } from './inbox';
import * as push from './push';
import { storage } from './storage';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-notifications', () => require('@/test-mocks').expoNotifications());
jest.mock('expo-router', () => ({ router: { push: jest.fn() }, useFocusEffect: jest.fn() }));

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'ios',
      session: async () => 'session-token',
      newIdempotencyKey: () => '01900000-0000-7000-8000-0000000000aa',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
  };
});

// Required, not imported: the linter resolves './push' to push.web.ts as well
// and would see one module imported twice.
const webPush: typeof import('./push.web') = require('./push.web');

const N = jest.mocked(Notifications);
const ID = '01900000-0000-7000-8000-000000000050';

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

const requests = () => mockFetch.mock.calls.map(([i]) => i as Request);
const writes = () => requests().filter((r) => r.method !== 'GET');

beforeEach(async () => {
  mockFetch.mockImplementation(async () => new Response(null, { status: 204 }));
  await storage.remove(push.DEVICE_KEY);
  await storage.remove(push.PUSH_KEY);
});

afterEach(() => {
  jest.restoreAllMocks();
  jest.clearAllMocks();
});

describe('push registration', () => {
  it('asks for the permission and registers the native token on iOS', async () => {
    expect(push.pushSupported()).toBe(true);
    expect(await push.enablePush()).toBe('registered');
    expect(N.requestPermissionsAsync).toHaveBeenCalledTimes(1);
    expect(N.getDevicePushTokenAsync).toHaveBeenCalledTimes(1);
    const [put] = writes();
    expect(put?.method).toBe('PUT');
    expect(new URL(put!.url).pathname).toMatch(/^\/v1\/me\/devices\/[0-9a-f-]{36}$/);
    expect(await put!.json()).toEqual({ platform: 'ios', pushToken: 'example-device-token-0001' });
    expect(await push.isPushOn()).toBe(true);
  });

  it('creates the Android channel first and sends platform android when the build has Firebase', async () => {
    jest.replaceProperty(Platform, 'OS', 'android');
    jest.replaceProperty(config, 'androidPush', true);
    expect(await push.enablePush()).toBe('registered');
    expect(N.setNotificationChannelAsync).toHaveBeenCalledWith('default', expect.anything());
    expect(await writes()[0]!.json()).toMatchObject({ platform: 'android' });
  });

  it('skips Android without a Firebase config: no prompt, no request', async () => {
    jest.replaceProperty(Platform, 'OS', 'android');
    expect(push.pushSupported()).toBe(false);
    expect(await push.enablePush()).toBe('unavailable');
    await push.offerPush();
    expect(N.requestPermissionsAsync).not.toHaveBeenCalled();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it('skips quietly when there is no token', async () => {
    N.getDevicePushTokenAsync.mockRejectedValueOnce(new Error('no firebase app'));
    expect(await push.enablePush()).toBe('unavailable');
    expect(mockFetch).not.toHaveBeenCalled();
    expect(await push.isPushOn()).toBe(false);
  });

  it('skips quietly when the server refuses the registration', async () => {
    mockFetch.mockResolvedValueOnce(json({ code: 'unavailable' }, 503));
    expect(await push.enablePush()).toBe('unavailable');
    expect(await push.isPushOn()).toBe(false);
  });

  it('reports a refused permission and remembers the choice', async () => {
    N.requestPermissionsAsync.mockResolvedValueOnce({ granted: false, canAskAgain: false, status: 'denied' } as never);
    expect(await push.enablePush()).toBe('denied');
    expect(N.getDevicePushTokenAsync).not.toHaveBeenCalled();
    expect(mockFetch).not.toHaveBeenCalled();
    // offerPush asks only a phone that was never asked.
    await push.offerPush();
    expect(N.requestPermissionsAsync).toHaveBeenCalledTimes(1);
  });

  it('never runs on the web', async () => {
    jest.replaceProperty(Platform, 'OS', 'web');
    expect(push.pushSupported()).toBe(false);
    expect(await push.enablePush()).toBe('unavailable');
    await push.refreshPush();
    await push.unregisterPush();
    // The web build loads push.web.ts, which has no native calls at all.
    expect(webPush.pushSupported()).toBe(false);
    expect(await webPush.enablePush()).toBe('unavailable');
    await webPush.offerPush();
    await webPush.unregisterPush();
    expect(N.getPermissionsAsync).not.toHaveBeenCalled();
    expect(N.getDevicePushTokenAsync).not.toHaveBeenCalled();
    expect(mockFetch).not.toHaveBeenCalled();
  });

  it('re-registers at start without asking, only while on and permitted', async () => {
    await push.refreshPush();
    expect(N.getPermissionsAsync).not.toHaveBeenCalled();
    await storage.set(push.PUSH_KEY, '1');
    await push.refreshPush();
    // Permission withdrawn in the settings: nothing is sent and nothing is asked.
    expect(N.requestPermissionsAsync).not.toHaveBeenCalled();
    expect(mockFetch).not.toHaveBeenCalled();
    N.getPermissionsAsync.mockResolvedValueOnce({ granted: true, canAskAgain: true, status: 'granted' } as never);
    await push.refreshPush();
    expect(writes().map((r) => r.method)).toEqual(['PUT']);
  });

  it('unregisters the same device ID and forgets it, keeping the choice', async () => {
    await push.enablePush();
    const putPath = new URL(writes()[0]!.url).pathname;
    await push.unregisterPush();
    const del = writes()[1]!;
    expect(del.method).toBe('DELETE');
    expect(new URL(del.url).pathname).toBe(putPath);
    expect(await storage.get(push.DEVICE_KEY)).toBeNull();
    expect(await push.isPushOn()).toBe(true);
    // Nothing registered: nothing to delete.
    await push.unregisterPush();
    expect(writes()).toHaveLength(2);
  });

  it('turning off stores the choice and deletes the device', async () => {
    await push.enablePush();
    await push.disablePush();
    expect(writes().map((r) => r.method)).toEqual(['PUT', 'DELETE']);
    expect(await push.isPushOn()).toBe(false);
  });
});

describe('tapped push', () => {
  it('reads the notification ID from FCM data or the APNs payload, and nothing else', () => {
    const tap = (request: unknown) => ({ notification: { request } });
    expect(push.notificationIdOf(tap({ content: { data: { notification_id: ID } } }))).toBe(ID);
    expect(push.notificationIdOf(tap({ content: { data: {} }, trigger: { payload: { notification_id: ID } } }))).toBe(
      ID,
    );
    expect(push.notificationIdOf(tap({ trigger: { remoteMessage: { data: { notification_id: ID } } } }))).toBe(ID);
    expect(push.notificationIdOf(tap({ content: { data: { notification_id: '../me' } } }))).toBeNull();
    expect(push.notificationIdOf(null)).toBeNull();
  });

  it('opens the target of the tapped item and marks it read', async () => {
    mockFetch.mockImplementation(async (input) => {
      const r = input as Request;
      if (r.method === 'GET')
        return json({
          items: [
            {
              id: ID,
              template: 'wellbeing.checkin_reminder',
              params: {},
              class: 'reminder',
              createdAt: '2026-10-28T10:00:00Z',
            },
          ],
        });
      return new Response(null, { status: 204 });
    });
    await openFromPush(new QueryClient(), ID);
    expect(router.push).toHaveBeenCalledWith('/checkin');
    await waitFor(() => expect(writes()).toHaveLength(1));
    expect(new URL(writes()[0]!.url).pathname).toBe(`/v1/me/inbox/${ID}/read`);
  });

  it('opens the inbox when the item is not found or the inbox cannot be read', async () => {
    mockFetch.mockResolvedValueOnce(json({ items: [] }));
    await openFromPush(new QueryClient(), ID);
    expect(router.push).toHaveBeenLastCalledWith('/inbox');
    mockFetch.mockResolvedValueOnce(json({ code: 'unavailable' }, 503));
    await openFromPush(new QueryClient(), ID);
    expect(router.push).toHaveBeenLastCalledWith('/inbox');
  });
});
