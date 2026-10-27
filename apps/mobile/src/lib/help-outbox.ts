// The app's outbox instance and its replay loop (ADR-0018 offline queue).
//
// A queued request is retried: right after it is queued, when the app returns
// to the foreground, and on a backoff timer while anything is pending. Each
// successful help request also refreshes the crisis phone list.
import { createOutbox, retryDelayMs, sendQueued } from '@birge/api';
import { AppState } from 'react-native';

import { api } from './api';
import { saveCrisisPhones } from './crisis-phones';
import { newIdempotencyKey } from './platform-crypto';
import { getSessionToken } from './session';
import { storage } from './storage';

/** outbox holds crisis requests until the server acknowledges them. */
export const outbox = createOutbox({
  store: storage,
  storageKey: 'birge.outbox',
  send: (entry) => sendQueued(api, entry),
  newKey: newIdempotencyKey,
});

outbox.subscribe((event) => {
  if (event.type === 'sent' && event.entry.op === 'requestHumanHelp') {
    const result = event.result as { crisisPhones?: unknown } | undefined;
    void saveCrisisPhones(result?.crisisPhones);
  }
});

let timer: ReturnType<typeof setTimeout> | null = null;
let failures = 0;

/**
 * flushOutbox sends what is pending and schedules the next attempt while
 * anything is left. Without a session nothing can succeed, so it waits for
 * the next sign-in instead of spending battery.
 */
export async function flushOutbox(): Promise<void> {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
  if (!getSessionToken()) return;
  const { remaining } = await outbox.flush();
  if (remaining === 0) {
    failures = 0;
    return;
  }
  failures++;
  timer = setTimeout(() => void flushOutbox(), retryDelayMs(failures));
}

/** startOutboxReplay starts the loop; it returns the stop function. */
export function startOutboxReplay(): () => void {
  void flushOutbox();
  const sub = AppState.addEventListener('change', (state) => {
    if (state === 'active') {
      failures = 0;
      void flushOutbox();
    }
  });
  return () => {
    sub.remove();
    if (timer) clearTimeout(timer);
    timer = null;
  };
}
