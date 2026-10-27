import { describe, expect, it } from '@jest/globals';

import { ApiError, NetworkError } from './errors';
import { createOutbox, memoryStore, retryDelayMs, type OutboxEntry, type OutboxEvent } from './outbox';

function setup(send: (e: OutboxEntry) => Promise<unknown>) {
  const store = memoryStore();
  let n = 0;
  const events: OutboxEvent[] = [];
  const outbox = createOutbox({ store, storageKey: 'outbox', send, newKey: () => `key-${++n}`, now: () => 1000 });
  outbox.subscribe((e) => events.push(e));
  return { store, outbox, events };
}

describe('outbox', () => {
  it('persists an entry before any send', async () => {
    const { store, outbox } = setup(async () => ({}));
    await outbox.enqueue('requestHumanHelp', { source: 'menu' });
    const raw = JSON.parse((await store.get('outbox')) ?? '{}');
    expect(raw).toEqual({
      v: 1,
      entries: [{ key: 'key-1', op: 'requestHumanHelp', body: { source: 'menu' }, createdAt: 1000, attempts: 0 }],
    });
  });

  it('keeps one pending help request (the server returns the open escalation anyway)', async () => {
    const { outbox } = setup(async () => ({}));
    const a = await outbox.enqueue('requestHumanHelp', { source: 'menu' });
    const b = await outbox.enqueue('requestHumanHelp', { source: 'checkin' });
    expect(b.key).toBe(a.key);
    expect(await outbox.pending()).toHaveLength(1);
  });

  it('removes an entry once sent and reports the result', async () => {
    const { outbox, events } = setup(async () => ({ escalationId: 'e1', crisisPhones: [] }));
    await outbox.enqueue('requestHumanHelp', { source: 'menu' });
    expect(await outbox.flush()).toEqual({ sent: 1, dropped: 0, remaining: 0 });
    expect(events).toEqual([
      expect.objectContaining({ type: 'sent', result: { escalationId: 'e1', crisisPhones: [] } }),
    ]);
  });

  it('keeps the entry and its key when offline, and replays it with the same key', async () => {
    const keys: string[] = [];
    let online = false;
    const { outbox } = setup(async (e) => {
      keys.push(e.key);
      if (!online) throw new NetworkError();
      return {};
    });
    await outbox.enqueue('requestHumanHelp', { source: 'menu' });
    expect(await outbox.flush()).toEqual({ sent: 0, dropped: 0, remaining: 1 });
    expect((await outbox.pending())[0]?.attempts).toBe(1);
    online = true;
    expect(await outbox.flush()).toEqual({ sent: 1, dropped: 0, remaining: 0 });
    expect(keys).toEqual(['key-1', 'key-1']);
  });

  it('keeps the entry on server errors and expired sessions', async () => {
    for (const status of [401, 429, 503]) {
      const { outbox } = setup(async () => {
        throw new ApiError(status, 'x', 'x');
      });
      await outbox.enqueue('requestHumanHelp', { source: 'menu' });
      expect((await outbox.flush()).remaining).toBe(1);
    }
  });

  it('drops an entry the server rejected for good', async () => {
    const { outbox, events } = setup(async () => {
      throw new ApiError(400, 'invalid_request', 'Bad');
    });
    await outbox.enqueue('requestHumanHelp', { source: 'menu' });
    expect(await outbox.flush()).toEqual({ sent: 0, dropped: 1, remaining: 0 });
    expect(events[0]?.type).toBe('dropped');
  });

  it('shares one run between concurrent flushes', async () => {
    let calls = 0;
    const { outbox } = setup(async () => {
      calls++;
      return {};
    });
    await outbox.enqueue('requestHumanHelp', { source: 'menu' });
    await Promise.all([outbox.flush(), outbox.flush()]);
    expect(calls).toBe(1);
  });

  it('reads entries written by an earlier version and ignores unknown or corrupt data', async () => {
    const store = memoryStore();
    await store.set(
      'outbox',
      JSON.stringify({
        v: 1,
        entries: [
          { key: 'old', op: 'requestHumanHelp', body: { source: 'sos' }, createdAt: 1, attempts: 3 },
          { key: 'future', op: 'somethingNew', body: {}, createdAt: 2, attempts: 0 },
        ],
      }),
    );
    const sent: string[] = [];
    const outbox = createOutbox({
      store,
      storageKey: 'outbox',
      newKey: () => 'k',
      send: async (e) => {
        sent.push(e.key);
        return {};
      },
    });
    await outbox.flush();
    expect(sent).toEqual(['old']);

    await store.set('outbox', 'not json');
    expect(await outbox.pending()).toEqual([]);
  });

  it('backs off from 5 s to 5 min', () => {
    expect(retryDelayMs(1)).toBe(5_000);
    expect(retryDelayMs(2)).toBe(10_000);
    expect(retryDelayMs(20)).toBe(300_000);
  });
});
