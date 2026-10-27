// Offline outbox for requests that must reach the server even when the phone
// is offline at the moment of the press: today "contact a person now" (the
// crisis path, ADR-0023); SOS joins it later.
//
// Guarantees:
//   - An entry is written to storage before the first send attempt, so killing
//     the app or losing the network right after the press loses nothing.
//   - Each entry keeps the Idempotency-Key it was created with; replays are
//     deduplicated by the server (ADR-0018).
//   - An entry leaves the outbox only when the server answered: success, or an
//     error that a retry cannot fix (see isRetryable). Retryable failures keep
//     it for the next flush.
//
// Compatibility (ADR-0018): entries are replayed by whatever app version runs
// next, possibly a newer one. The stored format (STORE_VERSION, operation
// names, bodies exactly as the contract defined them when queued) is
// therefore part of the contract: never rename an operation or change how an
// old entry is sent; add a new operation instead.
import { unwrap, type ApiClient, type ApiPaths } from './client';
import { isRetryable } from './errors';

type JsonBody<Op> = Op extends { requestBody: { content: { 'application/json': infer B } } } ? B : never;

/** QueueableBodies maps each queueable operation to its request body type from the contract. */
export interface QueueableBodies {
  requestHumanHelp: JsonBody<ApiPaths['/me/help-requests']['post']>;
}

/** QueueableOperation names a request the outbox can hold. Stored on disk: never rename. */
export type QueueableOperation = keyof QueueableBodies;

/** OutboxEntry is one stored request. */
export interface OutboxEntry<Op extends QueueableOperation = QueueableOperation> {
  /** key is the Idempotency-Key, also the entry's identity. */
  key: string;
  op: Op;
  body: QueueableBodies[Op];
  /** createdAt is Unix milliseconds of the press. */
  createdAt: number;
  /** attempts counts failed sends so far. */
  attempts: number;
}

/** KeyValueStore is the persistent storage the outbox writes to (secure store, localStorage, memory in tests). */
export interface KeyValueStore {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<void>;
}

/** OutboxEvent is emitted after a flush settles an entry. */
export type OutboxEvent =
  | { type: 'sent'; entry: OutboxEntry; result: unknown }
  | { type: 'dropped'; entry: OutboxEntry; error: unknown }
  | { type: 'retrying'; entry: OutboxEntry; error: unknown };

/** FlushResult summarises one flush. */
export interface FlushResult {
  sent: number;
  dropped: number;
  /** remaining is the number of entries still waiting (retryable failures). */
  remaining: number;
}

/** Sender sends one stored entry and resolves to the response data, throwing ApiError or NetworkError. */
export type Sender = (entry: OutboxEntry) => Promise<unknown>;

/** Outbox is the queue API used by the app. */
export interface Outbox {
  /**
   * enqueue stores a request and returns its entry. For a singleton operation
   * (one pending request is enough, e.g. requestHumanHelp: the server answers
   * with the already open escalation) the pending entry is returned instead.
   */
  enqueue<Op extends QueueableOperation>(op: Op, body: QueueableBodies[Op]): Promise<OutboxEntry<Op>>;
  /** flush sends pending entries in order. Concurrent calls share one run. */
  flush(): Promise<FlushResult>;
  /** pending lists the stored entries. */
  pending(): Promise<OutboxEntry[]>;
  /** subscribe registers a listener and returns the unsubscribe function. */
  subscribe(listener: (event: OutboxEvent) => void): () => void;
}

/** OutboxConfig configures createOutbox. */
export interface OutboxConfig {
  store: KeyValueStore;
  /** storageKey is where the queue lives in the store. */
  storageKey: string;
  send: Sender;
  newKey: () => string;
  now?: () => number;
  /** maxEntries bounds storage; the oldest entry is dropped beyond it. Default 50. */
  maxEntries?: number;
}

const STORE_VERSION = 1;
const SINGLETON_OPS: ReadonlySet<QueueableOperation> = new Set(['requestHumanHelp']);
const KNOWN_OPS: ReadonlySet<string> = new Set<QueueableOperation>(['requestHumanHelp']);

interface Stored {
  v: number;
  entries: OutboxEntry[];
}

/** sendQueued is the Sender for the real API: one case per operation, each with its original key. */
export function sendQueued(client: ApiClient, entry: OutboxEntry): Promise<unknown> {
  switch (entry.op) {
    case 'requestHumanHelp':
      return unwrap(
        client.POST('/me/help-requests', {
          body: (entry as OutboxEntry<'requestHumanHelp'>).body,
          headers: { 'Idempotency-Key': entry.key },
        }),
      );
  }
}

/**
 * retryDelayMs is the wait before flush attempt number `attempt` (1-based)
 * after failures: 5 s doubling to a 5 minute ceiling. Short at first because a
 * crisis request matters most in the first minutes; capped to spare battery.
 */
export function retryDelayMs(attempt: number): number {
  return Math.min(5_000 * 2 ** Math.max(0, attempt - 1), 300_000);
}

function isEntry(x: unknown): x is OutboxEntry {
  if (x === null || typeof x !== 'object') return false;
  const e = x as Record<string, unknown>;
  return (
    typeof e.key === 'string' &&
    typeof e.op === 'string' &&
    KNOWN_OPS.has(e.op) &&
    typeof e.createdAt === 'number' &&
    typeof e.attempts === 'number' &&
    e.body !== null &&
    typeof e.body === 'object'
  );
}

/** createOutbox builds an outbox over a persistent store. */
export function createOutbox(config: OutboxConfig): Outbox {
  const now = config.now ?? Date.now;
  const maxEntries = config.maxEntries ?? 50;
  const listeners = new Set<(event: OutboxEvent) => void>();

  // Storage is read-modify-write; this chain serialises those short critical
  // sections. Network calls run outside it, so a press is persisted at once
  // even while a slow flush is waiting for the server.
  let storageChain: Promise<unknown> = Promise.resolve();
  function withStorage<T>(fn: (entries: OutboxEntry[]) => Promise<{ entries?: OutboxEntry[]; value: T }>): Promise<T> {
    const run = storageChain.then(async () => {
      const entries = await read();
      const out = await fn(entries);
      if (out.entries) {
        const data: Stored = { v: STORE_VERSION, entries: out.entries };
        await config.store.set(config.storageKey, JSON.stringify(data));
      }
      return out.value;
    });
    storageChain = run.catch(() => undefined);
    return run;
  }

  async function read(): Promise<OutboxEntry[]> {
    const raw = await config.store.get(config.storageKey);
    if (!raw) return [];
    try {
      const parsed = JSON.parse(raw) as Partial<Stored>;
      // An entry this version cannot send could only come from a newer
      // version, and installed versions do not move backwards, so it is
      // discarded rather than blocking the queue.
      return Array.isArray(parsed.entries) ? parsed.entries.filter(isEntry) : [];
    } catch {
      return [];
    }
  }

  function emit(event: OutboxEvent): void {
    for (const l of listeners) l(event);
  }

  let flushing: Promise<FlushResult> | null = null;

  async function flushOnce(): Promise<FlushResult> {
    const result: FlushResult = { sent: 0, dropped: 0, remaining: 0 };
    const entries = await withStorage(async (e) => ({ value: e }));
    for (const entry of entries) {
      try {
        const data = await config.send(entry);
        await withStorage(async (current) => ({ entries: current.filter((e) => e.key !== entry.key), value: null }));
        result.sent++;
        emit({ type: 'sent', entry, result: data });
      } catch (error) {
        if (isRetryable(error)) {
          await withStorage(async (current) => ({
            entries: current.map((e) => (e.key === entry.key ? { ...e, attempts: e.attempts + 1 } : e)),
            value: null,
          }));
          emit({ type: 'retrying', entry, error });
          // Order matters (an older request first); and when one request
          // cannot get through, the next one will not either.
          break;
        }
        await withStorage(async (current) => ({ entries: current.filter((e) => e.key !== entry.key), value: null }));
        result.dropped++;
        emit({ type: 'dropped', entry, error });
      }
    }
    result.remaining = (await withStorage(async (e) => ({ value: e }))).length;
    return result;
  }

  return {
    enqueue(op, body) {
      return withStorage(async (entries) => {
        if (SINGLETON_OPS.has(op)) {
          const existing = entries.find((e) => e.op === op);
          if (existing) return { value: existing as OutboxEntry<typeof op> };
        }
        const entry: OutboxEntry<typeof op> = { key: config.newKey(), op, body, createdAt: now(), attempts: 0 };
        const next = [...entries, entry];
        while (next.length > maxEntries) {
          const dropped = next.shift();
          if (dropped) emit({ type: 'dropped', entry: dropped, error: new Error('outbox full') });
        }
        return { entries: next, value: entry };
      });
    },
    flush() {
      if (!flushing) {
        flushing = flushOnce().finally(() => {
          flushing = null;
        });
      }
      return flushing;
    },
    pending() {
      return withStorage(async (e) => ({ value: e }));
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
  };
}

/** memoryStore is a KeyValueStore in memory, for tests and as a last-resort fallback. */
export function memoryStore(): KeyValueStore {
  const m = new Map<string, string>();
  return {
    async get(k) {
      return m.get(k) ?? null;
    },
    async set(k, v) {
      m.set(k, v);
    },
  };
}
