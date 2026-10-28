import { describe, expect, it, jest } from '@jest/globals';

import { isCrisisPhone, parseConfig } from './config';
import { fetchDemoCode } from './demo-code';
import { categoryLabel, professionLabel, roomTitle } from './reference';
import { nextCheckin } from './next-checkin';
import { formatWhen } from './format';
import { isNotificationId, notificationTarget } from './notification-targets';
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
  const known = new Set(['templates.chat.new_message.title', 'templates.chat.new_message.body']);
  const exists = (k: string) => known.has(k);

  it('renders known templates and falls back for unknown or malformed ones', () => {
    expect(notificationKey('chat.new_message', exists)).toBe('templates.chat.new_message');
    expect(notificationKey('chat.unknown', exists)).toBe('fallback');
    expect(notificationKey('help:intro', () => true)).toBe('fallback');
    // A title without a body is not rendered half-way.
    expect(notificationKey('chat.new_message', (k) => k.endsWith('.title'))).toBe('fallback');
  });
});

describe('notificationTarget', () => {
  const room = '01900000-0000-7000-8000-000000000077';
  const n = (template: string, params: Record<string, string> = {}) => ({ template, params });

  it('maps known templates to their place in the app', () => {
    expect(notificationTarget(n('chat.new_message', { room_id: room }))).toBe(`/chat/${room}`);
    expect(notificationTarget(n('circle.match_proposed'))).toBe('/circle');
    expect(notificationTarget(n('emergency.sos_responded'))).toBe('/sos');
    expect(notificationTarget(n('sharing.contact_request'))).toBe('/listings');
    expect(notificationTarget(n('wellbeing.checkin_reminder'))).toBe('/checkin');
  });

  it('opens the trusted-contact side of the emergency plan by its id', () => {
    const id = '01900000-0000-7000-8000-0000000000c1';
    expect(notificationTarget(n('emergency.trusted_invitation', { contact_id: id }))).toBe(`/trusted-invitation/${id}`);
    expect(notificationTarget(n('emergency.plan_sent', { owner_id: id }))).toBe(`/shared-plan/${id}`);
    // Without a valid id there is no invitation or plan to open.
    expect(notificationTarget(n('emergency.trusted_invitation'))).toBeNull();
    expect(notificationTarget(n('emergency.trusted_invitation', { contact_id: '../sos' }))).toBeNull();
    expect(notificationTarget(n('emergency.plan_sent', { owner_id: `${id}/x` }))).toBeNull();
    // The id must come from the template's own param.
    expect(notificationTarget(n('emergency.plan_sent', { contact_id: id }))).toBeNull();
  });

  it('never builds a route from a param that is not a UUID', () => {
    expect(notificationTarget(n('chat.new_message', { room_id: '../../sos' }))).toBe('/chat');
    expect(notificationTarget(n('chat.new_message'))).toBe('/chat');
  });

  it('opens nothing for unknown templates and those without a screen', () => {
    expect(notificationTarget(n('future.thing'))).toBeNull();
    // No contract operation lets a contact read another member's SOS yet.
    expect(notificationTarget(n('emergency.sos_raised_contact', { sos_id: room }))).toBeNull();
    expect(notificationTarget(n('constructor'))).toBeNull();
    expect(notificationTarget(n('toString'))).toBeNull();
  });

  it('accepts only UUIDs as push notification IDs', () => {
    expect(isNotificationId(room)).toBe(true);
    expect(isNotificationId('1; DROP')).toBe(false);
    expect(isNotificationId(42)).toBe(false);
  });
});

describe('formatWhen', () => {
  const now = new Date(2026, 9, 28, 15, 0);

  it('shows the time today, the day this year and the full date before', () => {
    expect(formatWhen(new Date(2026, 9, 28, 9, 5).toISOString(), 'en', now)).toMatch(/09:05|9:05/);
    expect(formatWhen(new Date(2026, 9, 2, 9, 5).toISOString(), 'en', now)).toBe('Oct 2');
    expect(formatWhen(new Date(2025, 0, 2, 9, 5).toISOString(), 'en', now)).toBe('Jan 2, 2025');
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

describe('roomTitle', () => {
  it('names group rooms from their locale key and keeps direct rooms as they are', () => {
    expect(roomTitle('chat.room.district.demo_district_a')).toBe('Mothers of Demo district A');
    expect(roomTitle('chat.room.topic.autism')).toBe('Topic: Autism');
    expect(roomTitle('chat.room.topic.unknown_code')).toBe('Topic: unknown_code');
    expect(roomTitle('Dana')).toBe('Dana');
  });
});

describe('reference labels', () => {
  it('name professions and listing categories, and keep unknown codes', () => {
    expect(professionLabel('speech_therapist')).toBe('Speech therapist');
    expect(categoryLabel('stroller')).toBe('Stroller');
    expect(professionLabel('new_code')).toBe('new_code');
  });
});
