import { describe, expect, it, jest } from '@jest/globals';

import { canSeeTab, firstTabHref } from './staff';

// staff.ts also holds the queries; the pure helpers tested here need no client.
jest.mock('./api', () => ({ api: {} }));

describe('tabs by role', () => {
  it('shows each role only the tabs its API serves', () => {
    expect(canSeeTab('escalations', ['psychologist'])).toBe(true);
    expect(canSeeTab('escalations', ['admin'])).toBe(false);
    expect(canSeeTab('knowledge', ['moderator'])).toBe(false);
    expect(canSeeTab('knowledge', ['lawyer'])).toBe(true);
    expect(canSeeTab('moderation', ['psychologist'])).toBe(false);
    expect(canSeeTab('duty', ['lawyer'])).toBe(false);
    expect(canSeeTab('metrics', ['moderator', 'admin'])).toBe(true);
    expect(canSeeTab('sos', undefined)).toBe(false);
  });

  it('lands a role on its first tab', () => {
    expect(firstTabHref(['psychologist'])).toBe('/');
    expect(firstTabHref(['admin'])).toBe('/sos');
    expect(firstTabHref(['lawyer'])).toBe('/knowledge');
    expect(firstTabHref([])).toBe('/profile');
  });
});
