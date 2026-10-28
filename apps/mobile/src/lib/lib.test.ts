import { describe, expect, it, jest } from '@jest/globals';

import { isCrisisPhone, parseConfig } from './config';
import { fetchDemoCode } from './demo-code';
import { nextCheckin } from './next-checkin';
import { notificationKey } from './notification-text';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());

describe('parseConfig', () => {
  const extra = { apiUrl: 'https://api.example.invalid/v1/', kratosUrl: 'https://api.example.invalid/auth' };

  it('validates URLs, version and platform', () => {
    const c = parseConfig(extra, '1.2.3', 'android');
    expect(c).toMatchObject({ apiUrl: 'https://api.example.invalid/v1', appVersion: '1.2.3', platform: 'android' });
    expect(parseConfig(extra, 'dev', 'macos')).toMatchObject({ appVersion: '0.0.0', platform: 'web' });
    expect(() => parseConfig({ ...extra, apiUrl: 'ftp://x' }, '1.0.0', 'ios')).toThrow('http(s)');
    expect(() => parseConfig({}, '1.0.0', 'ios')).toThrow('missing');
  });

  it('keeps only valid fallback crisis phones', () => {
    const crisisPhones = JSON.stringify([
      { number: '+70000000000', label: 'Line' },
      { number: 'javascript:alert(1)', label: 'Bad' },
      'nope',
    ]);
    expect(parseConfig({ ...extra, crisisPhones }, '1.0.0', 'ios').crisisPhones).toEqual([
      { number: '+70000000000', label: 'Line' },
    ]);
    expect(parseConfig({ ...extra, crisisPhones: 'not json' }, '1.0.0', 'ios').crisisPhones).toEqual([]);
    expect(isCrisisPhone({ number: '112', label: 'x'.repeat(121) })).toBe(false);
  });
});

describe('nextCheckin', () => {
  // Wednesday 2026-01-07 10:30 local time.
  const now = new Date(2026, 0, 7, 10, 30);

  it('is null when reminders are off or not set', () => {
    expect(nextCheckin(undefined, now)).toBeNull();
    expect(nextCheckin({}, now)).toBeNull();
    expect(nextCheckin({ weekday: 3, hour: 9, enabled: false }, now)).toBeNull();
  });

  it('finds the next matching weekday and hour', () => {
    expect(nextCheckin({ weekday: 5, hour: 18 }, now)).toEqual(new Date(2026, 0, 9, 18, 0));
    expect(nextCheckin({ weekday: 3, hour: 12 }, now)).toEqual(new Date(2026, 0, 7, 12, 0));
    // Earlier today: next week.
    expect(nextCheckin({ weekday: 3, hour: 9 }, now)).toEqual(new Date(2026, 0, 14, 9, 0));
    // ISO 7 is Sunday.
    expect(nextCheckin({ weekday: 7, hour: 8, enabled: true }, now)).toEqual(new Date(2026, 0, 11, 8, 0));
  });
});

describe('notificationKey', () => {
  const known = new Set(['templates.chat.new_message']);
  const exists = (k: string) => known.has(k);

  it('renders known templates and falls back for unknown or malformed ones', () => {
    expect(notificationKey('chat.new_message', exists)).toBe('templates.chat.new_message');
    expect(notificationKey('chat.unknown', exists)).toBe('fallback');
    expect(notificationKey('help:intro', () => true)).toBe('fallback');
  });
});

describe('demo codes', () => {
  const extra = { apiUrl: 'https://api.example.invalid/v1', kratosUrl: 'https://api.example.invalid/auth' };

  it('are off unless the build sets a URL', () => {
    expect(parseConfig(extra, '1.0.0', 'android').demoCodesUrl).toBeUndefined();
    expect(parseConfig({ ...extra, demoCodesUrl: '' }, '1.0.0', 'android').demoCodesUrl).toBeUndefined();
    expect(
      parseConfig({ ...extra, demoCodesUrl: 'https://demo.example.invalid/demo/code' }, '1.0.0', 'android')
        .demoCodesUrl,
    ).toBe('https://demo.example.invalid/demo/code');
  });

  it('accept only a code-shaped answer', async () => {
    const realFetch = global.fetch;
    const answer = (status: number, body: unknown) =>
      jest.fn(async () => ({ ok: status === 200, status, json: async () => body }) as unknown as Response);
    try {
      global.fetch = answer(200, { code: '482913' }) as unknown as typeof fetch;
      expect(await fetchDemoCode('https://d.example.invalid/demo/code', '+77000000012')).toBe('482913');
      global.fetch = answer(200, { code: '<b>x</b>' }) as unknown as typeof fetch;
      expect(await fetchDemoCode('https://d.example.invalid/demo/code', '+77000000012')).toBeNull();
      global.fetch = answer(404, null) as unknown as typeof fetch;
      expect(await fetchDemoCode('https://d.example.invalid/demo/code', '+77011234567')).toBeNull();
      global.fetch = jest.fn(async () => {
        throw new Error('offline');
      }) as unknown as typeof fetch;
      expect(await fetchDemoCode('https://d.example.invalid/demo/code', '+77000000012')).toBeNull();
    } finally {
      global.fetch = realFetch;
    }
  });
});
