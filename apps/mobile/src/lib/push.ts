// Push on iOS and Android (notify context, registerDevice / unregisterDevice).
// The web build uses push.web.ts, which does nothing.
//
// - Native tokens only (getDevicePushTokenAsync: FCM on Android, APNs on iOS):
//   the backend talks to FCM and APNs directly, with no Expo push service.
// - A push carries only the notification ID and a neutral title (ADR-0013,
//   AGENTS.md N3); the text is rendered from the inbox. The app never
//   schedules a local notification, so no content reaches the system tray.
// - The permission is asked at a calm moment (after the first-run setup, or
//   from the profile toggle), never on first launch.
// - No Firebase config in the build, a refused permission or any token error
//   skips quietly: no banner, no crash, nothing logged. The inbox still works.
//   The push token is a secret of this device: never logged or stored here.
// - The device ID is a random UUID made per sign-in and forgotten at sign-out
//   (or when the session expires), so a second member on the same phone never
//   writes to the first member's device row.
import { unwrap } from '@birge/api';
import { i18n } from '@birge/i18n';
import type { QueryClient } from '@tanstack/react-query';
import * as Notifications from 'expo-notifications';
import { router, type Href } from 'expo-router';
import { useEffect, useRef } from 'react';
import { Platform } from 'react-native';

import { api } from './api';
import { config } from './config';
import { inboxKey, openFromPush } from './inbox';
import { isNotificationId } from './notification-targets';
import { newIdempotencyKey } from './platform-crypto';
import { storage } from './storage';

/** DEVICE_KEY stores the device ID registered for the signed-in member. */
export const DEVICE_KEY = 'birge.push.device';
/** PUSH_KEY stores the choice for this phone: '1' on, '0' off, absent = not asked yet. */
export const PUSH_KEY = 'birge.push.enabled';

/** ANDROID_CHANNEL is the notification channel; app.config.ts names it as the default. */
const ANDROID_CHANNEL = 'default';

/** PushResult is how an attempt to turn push on ended. */
export type PushResult = 'registered' | 'denied' | 'unavailable';

/** pushSupported is true where this build can receive push: iOS, and Android with a Firebase config. */
export function pushSupported(): boolean {
  if (Platform.OS === 'ios') return true;
  if (Platform.OS === 'android') return config.androidPush;
  return false;
}

// One operation at a time: a quick on-off-on on the toggle, or a sign-out
// during a registration, must not interleave PUT and DELETE.
let queue: Promise<unknown> = Promise.resolve();
function serial<T>(fn: () => Promise<T>): Promise<T> {
  const run = queue.then(fn, fn);
  queue = run.catch(() => undefined);
  return run;
}

async function read(key: string): Promise<string | null> {
  try {
    return await storage.get(key);
  } catch {
    return null;
  }
}

async function write(key: string, value: string | null): Promise<void> {
  try {
    if (value === null) await storage.remove(key);
    else await storage.set(key, value);
  } catch {
    // Not remembered: the next start asks the server again, nothing worse.
  }
}

async function deviceId(): Promise<string> {
  const stored = await read(DEVICE_KEY);
  if (stored) return stored;
  const id = newIdempotencyKey();
  await write(DEVICE_KEY, id);
  return id;
}

/** register sends this device's token; any failure is 'unavailable' and stays quiet. */
async function register(): Promise<PushResult> {
  try {
    const token = await Notifications.getDevicePushTokenAsync();
    if (typeof token.data !== 'string' || token.data.length < 8) return 'unavailable';
    const id = await deviceId();
    await unwrap(
      api.PUT('/me/devices/{deviceId}', {
        params: { path: { deviceId: id } },
        body: { platform: Platform.OS === 'ios' ? 'ios' : 'android', pushToken: token.data },
      }),
    );
    return 'registered';
  } catch {
    return 'unavailable';
  }
}

async function unregister(): Promise<void> {
  const id = await read(DEVICE_KEY);
  await write(DEVICE_KEY, null);
  if (!id) return;
  try {
    await unwrap(api.DELETE('/me/devices/{deviceId}', { params: { path: { deviceId: id } } }));
  } catch {
    // Best effort: a stale row is removed by the server when the provider
    // rejects the token, and it only ever carries a content-free push.
  }
}

async function granted(ask: boolean): Promise<boolean> {
  try {
    // Android 13+ shows the permission prompt only once a channel exists.
    if (Platform.OS === 'android') {
      await Notifications.setNotificationChannelAsync(ANDROID_CHANNEL, {
        name: i18n.t('inbox:title'),
        // The linter's resolver picks the web build of expo-notifications,
        // which lacks the Android enum; TypeScript checks the real export.
        // eslint-disable-next-line import/namespace
        importance: Notifications.AndroidImportance.HIGH,
      });
    }
    const now = await Notifications.getPermissionsAsync();
    if (now.granted || !ask || !now.canAskAgain) return now.granted;
    return (await Notifications.requestPermissionsAsync()).granted;
  } catch {
    return false;
  }
}

/** isPushOn reports the choice stored for this phone. */
export async function isPushOn(): Promise<boolean> {
  return (await read(PUSH_KEY)) === '1';
}

/**
 * enablePush asks for the permission if needed and registers the device.
 * The choice is stored as on only when the registration succeeded.
 */
export function enablePush(): Promise<PushResult> {
  return serial(async () => {
    if (!pushSupported()) return 'unavailable';
    if (!(await granted(true))) {
      await write(PUSH_KEY, '0');
      return 'denied';
    }
    const result = await register();
    if (result === 'registered') await write(PUSH_KEY, '1');
    return result;
  });
}

/** disablePush turns push off for this phone and removes the device registration. */
export function disablePush(): Promise<void> {
  return serial(async () => {
    await write(PUSH_KEY, '0');
    await unregister();
  });
}

/**
 * offerPush asks once, after the first-run setup: only when this phone has
 * never been asked. A choice made earlier, either way, is kept.
 */
export async function offerPush(): Promise<void> {
  if (!pushSupported() || (await read(PUSH_KEY)) !== null) return;
  await enablePush();
}

/**
 * refreshPush registers again after sign-in and on every start while push is
 * on, without asking: tokens rotate, and the server drops a token a provider
 * rejected. If the permission was withdrawn in the phone settings, nothing
 * is sent.
 */
export function refreshPush(): Promise<void> {
  return serial(async () => {
    if (!pushSupported() || !(await isPushOn()) || !(await granted(false))) return;
    await register();
  });
}

/**
 * unregisterPush removes this device's registration while the session is
 * still valid: sign-out and account deletion (both go through
 * SessionProvider's beforeSignOut). The choice for the phone is kept.
 */
export function unregisterPush(): Promise<void> {
  return serial(async () => {
    if (!pushSupported()) return;
    await unregister();
  });
}

/** forgetDevice drops the local device ID after the session ended without sign-out (expired). */
function forgetDevice(): Promise<void> {
  return serial(() => write(DEVICE_KEY, null));
}

function field(obj: unknown, ...path: string[]): unknown {
  let cur = obj;
  for (const k of path) {
    if (cur === null || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[k];
  }
  return cur;
}

/**
 * notificationIdOf reads the notification ID from a tapped push: FCM puts it
 * in `data`, APNs at the top level of the payload. Anything that is not a
 * UUID is ignored.
 */
export function notificationIdOf(response: unknown): string | null {
  const request = field(response, 'notification', 'request');
  const candidates = [
    field(request, 'content', 'data', 'notification_id'),
    field(request, 'trigger', 'payload', 'notification_id'),
    field(request, 'trigger', 'remoteMessage', 'data', 'notification_id'),
  ];
  return candidates.find(isNotificationId) ?? null;
}

// Taps already handled in this process: getLastNotificationResponseAsync
// keeps returning the launch tap, and the bridge re-runs on every sign-in.
const handled = new Set<string>();

function openResponse(client: QueryClient, response: Notifications.NotificationResponse): void {
  const key = response.notification.request.identifier;
  if (handled.has(key)) return;
  handled.add(key);
  const id = notificationIdOf(response);
  if (id) void openFromPush(client, id);
  else router.push('/inbox' as Href);
}

/**
 * usePushBridge connects push to the app while a member is signed in: shows
 * a push that arrives in the foreground (its neutral title only) and
 * refreshes the inbox, opens the item of a tapped push, re-registers after
 * sign-in and forgets the device ID when the session ends.
 */
export function usePushBridge(signedIn: boolean, hydrated: boolean, client: QueryClient): void {
  const wasSignedIn = useRef(false);

  useEffect(() => {
    if (!pushSupported()) return;
    Notifications.setNotificationHandler({
      handleNotification: async () => ({
        shouldShowBanner: true,
        shouldShowList: true,
        shouldPlaySound: true,
        shouldSetBadge: false,
      }),
    });
    const received = Notifications.addNotificationReceivedListener(() => {
      void client.invalidateQueries({ queryKey: inboxKey });
    });
    return () => received.remove();
  }, [client]);

  useEffect(() => {
    if (!pushSupported() || !hydrated) return;
    if (!signedIn) {
      if (wasSignedIn.current) void forgetDevice();
      wasSignedIn.current = false;
      return;
    }
    wasSignedIn.current = true;
    let active = true;
    void refreshPush();
    void Notifications.getLastNotificationResponseAsync()
      .then((r) => {
        if (r && active) openResponse(client, r);
      })
      .catch(() => undefined);
    const tapped = Notifications.addNotificationResponseReceivedListener((r) => openResponse(client, r));
    return () => {
      active = false;
      tapped.remove();
    };
  }, [signedIn, hydrated, client]);
}
