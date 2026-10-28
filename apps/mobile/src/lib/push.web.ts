// Web (PWA) build: no push. The browser would need a web push service, which
// the backend does not use (FCM and APNs only), so the web build never asks
// for a permission and never loads expo-notifications. Same exports as
// push.ts; the inbox works the same without push.
import type { PushResult } from './push';

export type { PushResult } from './push';

/** DEVICE_KEY: see push.ts. */
export const DEVICE_KEY = 'birge.push.device';
/** PUSH_KEY: see push.ts. */
export const PUSH_KEY = 'birge.push.enabled';

/** pushSupported is always false on the web. */
export const pushSupported = (): boolean => false;
/** isPushOn is always false on the web. */
export const isPushOn = async (): Promise<boolean> => false;
/** enablePush does nothing on the web. */
export const enablePush = async (): Promise<PushResult> => 'unavailable';
/** disablePush does nothing on the web. */
export const disablePush = async (): Promise<void> => undefined;
/** offerPush does nothing on the web. */
export const offerPush = async (): Promise<void> => undefined;
/** refreshPush does nothing on the web. */
export const refreshPush = async (): Promise<void> => undefined;
/** unregisterPush does nothing on the web. */
export const unregisterPush = async (): Promise<void> => undefined;
/** notificationIdOf: no push on the web. */
export const notificationIdOf = (): string | null => null;
/** usePushBridge does nothing on the web. */
export function usePushBridge(): void {}
