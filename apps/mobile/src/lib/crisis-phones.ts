// The last known crisis phone numbers. The help screen shows them at once,
// before and regardless of any network call (ADR-0023, the help-requests
// contract: "it never waits for this call to show them").
//
// Sources, newest wins: the build-time fallback (config), the client-config
// answer at startup, and every help-request answer. Each list is validated
// before it is stored or dialled.
import { unwrap } from '@birge/api';
import { useSyncExternalStore } from 'react';

import { api } from './api';
import { config, isCrisisPhone, type CrisisPhone } from './config';
import { storage } from './storage';

const KEY = 'birge.crisisPhones';

let cache: CrisisPhone[] | null = null;
const listeners = new Set<() => void>();

function valid(list: unknown): CrisisPhone[] | null {
  if (!Array.isArray(list)) return null;
  const phones = list.filter(isCrisisPhone).slice(0, 10);
  return phones.length > 0 ? phones : null;
}

/** currentCrisisPhones returns the best list known right now, synchronously. */
export function currentCrisisPhones(): CrisisPhone[] {
  return cache ?? config.crisisPhones;
}

/** loadCrisisPhones reads the stored list into memory (called once at startup). */
export async function loadCrisisPhones(): Promise<CrisisPhone[]> {
  try {
    const raw = await storage.get(KEY);
    const stored = raw ? valid(JSON.parse(raw)) : null;
    if (stored && cache === null) setCache(stored);
  } catch {
    // Unreadable storage: keep the fallback; the next server answer repairs it.
  }
  return currentCrisisPhones();
}

/** saveCrisisPhones stores a list from the server. An empty or invalid list never replaces a good one. */
export async function saveCrisisPhones(list: unknown): Promise<void> {
  const phones = valid(list);
  if (!phones) return;
  setCache(phones);
  await storage.set(KEY, JSON.stringify(phones)).catch(() => undefined);
}

/** refreshCrisisPhones asks /meta/client-config (no session needed) and stores the answer. */
export async function refreshCrisisPhones(): Promise<void> {
  try {
    const cfg = await unwrap(api.GET('/meta/client-config'));
    await saveCrisisPhones(cfg.crisisPhones);
  } catch {
    // Offline or server down: the stored list stays. Retried at next start.
  }
}

function setCache(phones: CrisisPhone[]): void {
  cache = phones;
  for (const l of listeners) l();
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange);
  return () => {
    listeners.delete(onChange);
  };
}

/** useCrisisPhones returns the current list and follows updates. */
export function useCrisisPhones(): CrisisPhone[] {
  return useSyncExternalStore(subscribe, currentCrisisPhones, currentCrisisPhones);
}
