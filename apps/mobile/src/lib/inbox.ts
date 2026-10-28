// The member's inbox (notify context, listInbox and markRead): the queries the
// inbox screen, the Home preview and the unread badge share, and what opening
// an item does. Every query lives under inboxKey, so one invalidation (screen
// focus, a push received in the foreground, markRead) refreshes all of them.
// Items carry a template and IDs only; the text comes from the locale files.
import { unwrap, type Schemas } from '@birge/api';
import { useInfiniteQuery, useQuery, useQueryClient, type InfiniteData, type QueryClient } from '@tanstack/react-query';
import { router, useFocusEffect, type Href } from 'expo-router';
import { useCallback, useRef } from 'react';

import { api } from './api';
import { notificationTarget } from './notification-targets';

/** Notification is one inbox item. */
export type Notification = Schemas['Notification'];

type InboxPage = Schemas['InboxPage'];

/** inboxKey is the query key prefix of every inbox read. */
export const inboxKey = ['inbox'] as const;

/** INBOX_PAGE is the page size of the inbox screen (the contract's default). */
export const INBOX_PAGE = 20;

/**
 * BADGE_WINDOW is how many of the newest items the unread badge looks at (the
 * contract's maximum page). The badge is a nudge, not an exact counter: more
 * unread items than this show as "9+" anyway.
 */
export const BADGE_WINDOW = 50;

/** BADGE_MAX is the largest number the badge spells out. */
const BADGE_MAX = 9;

function fetchPage(limit: number, cursor?: string): Promise<InboxPage> {
  return unwrap(api.GET('/me/inbox', { params: { query: { limit, ...(cursor ? { cursor } : {}) } } }));
}

// staleTime 0: the inbox is refetched every time the app returns to the
// foreground (focusManager in query.ts refetches only stale queries).
const fresh = { staleTime: 0 } as const;

/** useInbox pages through the inbox, newest first ("Show more" loads the next cursor). */
export function useInbox() {
  return useInfiniteQuery({
    queryKey: [...inboxKey, 'list'],
    queryFn: ({ pageParam }) => fetchPage(INBOX_PAGE, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor || undefined,
    ...fresh,
  });
}

/** useInboxPreview loads the newest few items (Home). */
export function useInboxPreview(limit: number) {
  return useQuery({ queryKey: [...inboxKey, 'preview', limit], queryFn: () => fetchPage(limit), ...fresh });
}

/** unreadCount counts unread items of a page. */
export function unreadCount(page: InboxPage | undefined): number {
  return page ? page.items.filter((n) => !n.readAt).length : 0;
}

/** badgeText is what the badge shows: nothing for zero, "9+" above BADGE_MAX. */
export function badgeText(count: number): string | undefined {
  if (count <= 0) return undefined;
  return count > BADGE_MAX ? `${BADGE_MAX}+` : String(count);
}

/** useUnreadCount is the number of unread items among the newest BADGE_WINDOW (0 until loaded or on error). */
export function useUnreadCount(enabled = true): number {
  const q = useQuery({
    queryKey: [...inboxKey, 'unread'],
    queryFn: () => fetchPage(BADGE_WINDOW),
    select: unreadCount,
    enabled,
    ...fresh,
  });
  return q.data ?? 0;
}

/**
 * useRefreshInboxOnFocus refetches every inbox query when the screen comes
 * back into focus (the first focus is the mount, which fetches anyway).
 */
export function useRefreshInboxOnFocus(): void {
  const client = useQueryClient();
  const first = useRef(true);
  useFocusEffect(
    useCallback(() => {
      if (first.current) {
        first.current = false;
        return;
      }
      void client.invalidateQueries({ queryKey: inboxKey });
    }, [client]),
  );
}

type Cached = InboxPage | InfiniteData<InboxPage> | undefined;

function markInPage(page: InboxPage, id: string, at: string): InboxPage {
  return { ...page, items: page.items.map((n) => (n.id === id && !n.readAt ? { ...n, readAt: at } : n)) };
}

/** markCachedRead sets readAt on one item in a cached page or page list. */
export function markCachedRead(data: Cached, id: string, at: string): Cached {
  if (!data) return data;
  if ('pages' in data) return { ...data, pages: data.pages.map((p) => markInPage(p, id, at)) };
  return markInPage(data, id, at);
}

/**
 * markRead marks one item read (idempotent on the server; for a critical
 * notification it also stops the SMS fallback). The cache is updated at once
 * so the unread style and the badge follow the tap; whatever the answer, the
 * inbox is then refetched so a failed call shows the item unread again.
 */
export async function markRead(client: QueryClient, id: string): Promise<void> {
  client.setQueriesData<Cached>({ queryKey: inboxKey }, (d) => markCachedRead(d, id, new Date().toISOString()));
  try {
    await unwrap(api.POST('/me/inbox/{notificationId}/read', { params: { path: { notificationId: id } } }));
  } catch {
    // Nothing to tell her: the refetch below shows the true state.
  } finally {
    await client.invalidateQueries({ queryKey: inboxKey });
  }
}

/**
 * openNotification marks an unread item read and goes to its place in the
 * app, if the template has one (notification-targets.ts). It returns the
 * route it opened, or null.
 */
export function openNotification(client: QueryClient, n: Notification): string | null {
  if (!n.readAt) void markRead(client, n.id);
  const target = notificationTarget(n);
  if (target) router.push(target as Href);
  return target;
}

/**
 * openFromPush handles a tap on a push, which carries only the notification
 * ID: it looks the item up among the newest ones and opens it like a tap in
 * the inbox. When the item has no place in the app, is not found or the
 * inbox cannot be read, the inbox opens.
 */
export async function openFromPush(client: QueryClient, id: string): Promise<void> {
  let item: Notification | undefined;
  try {
    item = (await fetchPage(BADGE_WINDOW)).items.find((n) => n.id === id);
  } catch {
    item = undefined;
  }
  if (item && openNotification(client, item)) return;
  router.push('/inbox' as Href);
}
