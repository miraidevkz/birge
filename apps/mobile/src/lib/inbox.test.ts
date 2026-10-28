import { describe, expect, it, jest } from '@jest/globals';

import { badgeText, markCachedRead, unreadCount } from './inbox';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({ router: { push: jest.fn() }, useFocusEffect: jest.fn() }));

const at = '2026-10-28T10:00:00Z';
const item = (id: string, readAt?: string) => ({
  id,
  template: 'circle.ask_met',
  params: {},
  class: 'normal' as const,
  createdAt: at,
  ...(readAt ? { readAt } : {}),
});

describe('unread badge', () => {
  it('counts unread items and shows nothing for zero, the number up to 9, then 9+', () => {
    expect(unreadCount(undefined)).toBe(0);
    expect(unreadCount({ items: [item('a'), item('b', at), item('c')] })).toBe(2);
    expect(badgeText(0)).toBeUndefined();
    expect(badgeText(3)).toBe('3');
    expect(badgeText(9)).toBe('9');
    expect(badgeText(10)).toBe('9+');
  });
});

describe('markCachedRead', () => {
  it('marks one item read in a page and in a page list, leaving read items alone', () => {
    const page = { items: [item('a'), item('b', '2026-10-27T00:00:00Z')] };
    const later = '2026-10-28T12:00:00Z';
    expect(markCachedRead(page, 'a', later)).toEqual({
      items: [item('a', later), item('b', '2026-10-27T00:00:00Z')],
    });
    expect(markCachedRead(page, 'b', later)).toEqual(page);
    const pages = { pages: [page, { items: [item('c')] }], pageParams: [undefined, 'c2'] };
    expect(unreadCount((markCachedRead(pages, 'c', later) as typeof pages).pages[1])).toBe(0);
    expect(markCachedRead(undefined, 'a', later)).toBeUndefined();
  });
});
