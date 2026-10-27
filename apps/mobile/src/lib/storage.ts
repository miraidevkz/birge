// Persistent key-value storage on iOS and Android: the platform keystore
// through expo-secure-store, for the session token and for the crisis outbox
// and phone list (small values, and they say something about the member).
// The web build uses storage.web.ts instead.
import type { KeyValueStore } from '@birge/api';
import * as SecureStore from 'expo-secure-store';

// Readable after the first unlock, so a queued crisis request can be replayed
// when the app wakes in the background.
const options: SecureStore.SecureStoreOptions = { keychainAccessible: SecureStore.AFTER_FIRST_UNLOCK };

/** Storage adds delete to the outbox's KeyValueStore. Keys use [A-Za-z0-9._-] only. */
export interface Storage extends KeyValueStore {
  remove(key: string): Promise<void>;
}

/** storage is the device's persistent store. */
export const storage: Storage = {
  get: (key) => SecureStore.getItemAsync(key, options),
  set: (key, value) => SecureStore.setItemAsync(key, value, options),
  remove: (key) => SecureStore.deleteItemAsync(key, options),
};
