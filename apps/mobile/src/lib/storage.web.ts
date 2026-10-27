// Web (PWA) storage: expo-secure-store has no web implementation, so the
// browser's localStorage is used. It is readable by any script on the origin,
// which is why the web build must ship a strict Content-Security-Policy and no
// third-party scripts. A private window or blocked storage falls back to
// memory: the app still works, only nothing survives a reload.
import type { Storage } from './storage';

const memory = new Map<string, string>();

function local(): globalThis.Storage | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}

/** storage is the browser's persistent store. */
export const storage: Storage = {
  async get(key) {
    try {
      return local()?.getItem(key) ?? memory.get(key) ?? null;
    } catch {
      return memory.get(key) ?? null;
    }
  },
  async set(key, value) {
    memory.set(key, value);
    try {
      local()?.setItem(key, value);
    } catch {
      // Quota or privacy mode: memory keeps it for this session.
    }
  },
  async remove(key) {
    memory.delete(key);
    try {
      local()?.removeItem(key);
    } catch {
      // Same as above.
    }
  },
};
