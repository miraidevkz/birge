import { resources } from '@birge/i18n';
import { describe, expect, it, jest } from '@jest/globals';

import {
  AUDIT_ACTIONS,
  AUDIT_OBJECTS,
  AUDIT_REASONS,
  AUDIT_ROLES,
  auditLabelKey,
  auditRoleKey,
  shortId,
} from './audit-codes';
import { auditObjectLink } from './audit-links';
import { isAdmin, staffName } from './staff';
import { dayEnd, dayStart, isDay } from './time';

// staff.ts also holds the queries; the pure helpers tested here need no client.
jest.mock('./api', () => ({ api: {} }));

function lookup(tree: unknown, key: string): unknown {
  return key.split('.').reduce<unknown>((node, part) => {
    if (node && typeof node === 'object') return (node as Record<string, unknown>)[part];
    return undefined;
  }, tree);
}

describe('auditLabelKey', () => {
  it('maps a known code to a locale key without the code dots', () => {
    expect(auditLabelKey('action', 'escalation.take')).toBe('audit.action.escalation_take');
    expect(auditLabelKey('object', 'chat.room')).toBe('audit.object.chat_room');
    expect(auditLabelKey('reason', 'outdated_law')).toBe('audit.reason.outdated_law');
  });

  it('returns undefined for an unknown code or a code of another kind', () => {
    expect(auditLabelKey('action', 'future.thing')).toBeUndefined();
    expect(auditLabelKey('object', 'escalation.take')).toBeUndefined();
    expect(auditLabelKey('reason', '')).toBeUndefined();
  });

  it('has a label in every language for every known code', () => {
    const lists = [
      ['action', AUDIT_ACTIONS],
      ['object', AUDIT_OBJECTS],
      ['reason', AUDIT_REASONS],
    ] as const;
    for (const lang of ['kk', 'ru', 'en'] as const) {
      const admin: unknown = resources[lang].admin;
      for (const [kind, codes] of lists) {
        for (const code of codes) {
          const key = auditLabelKey(kind, code);
          expect(key).toBeDefined();
          expect(typeof lookup(admin, key ?? '')).toBe('string');
        }
      }
    }
  });
});

describe('auditRoleKey', () => {
  it('names the known roles, including the external trusted contact', () => {
    expect(auditRoleKey('admin')).toBe('audit.role.admin');
    expect(auditRoleKey('external_contact')).toBe('audit.role.external_contact');
  });

  it('returns undefined for a role the console does not know', () => {
    expect(auditRoleKey('future_role')).toBeUndefined();
    expect(auditRoleKey('toString')).toBeUndefined();
  });

  it('has a label in every language for every known role and the generic one', () => {
    for (const lang of ['kk', 'ru', 'en'] as const) {
      const admin: unknown = resources[lang].admin;
      for (const role of AUDIT_ROLES) expect(typeof lookup(admin, auditRoleKey(role) ?? '')).toBe('string');
      expect(typeof lookup(admin, 'audit.unknown.role')).toBe('string');
    }
  });
});

describe('auditObjectLink', () => {
  it('links the object types the console has a screen for', () => {
    expect(auditObjectLink('safety.escalation')).toEqual({ href: '/', tab: 'escalations' });
    expect(auditObjectLink('membership.member')).toEqual({ href: '/members', tab: 'members' });
    expect(auditObjectLink('chat.member')).toEqual({ href: '/members', tab: 'members' });
    expect(auditObjectLink('chat.room')).toEqual({ href: '/rooms', tab: 'rooms' });
    expect(auditObjectLink('moderation.item')).toEqual({ href: '/moderation', tab: 'moderation' });
  });

  it('has no link for objects without a screen or unknown types', () => {
    expect(auditObjectLink('chat.message')).toBeUndefined();
    expect(auditObjectLink('emergency.child_card')).toBeUndefined();
    expect(auditObjectLink('future.object')).toBeUndefined();
    expect(auditObjectLink('toString')).toBeUndefined();
  });
});

describe('staff helpers', () => {
  it('names staff by the IdP name, else a short ID', () => {
    expect(staffName('01900000-0000-7000-8000-0000000000e1', 'Example Admin')).toBe('Example Admin');
    expect(staffName('01900000-0000-7000-8000-0000000000e1', '  ')).toBe('01900000');
    expect(staffName('01900000-0000-7000-8000-0000000000e1')).toBe(shortId('01900000-0000-7000-8000-0000000000e1'));
  });

  it('shows the admin tab only for the admin role', () => {
    expect(isAdmin(['moderator', 'admin'])).toBe(true);
    expect(isAdmin(['moderator'])).toBe(false);
    expect(isAdmin(undefined)).toBe(false);
  });
});

describe('day range', () => {
  it('accepts only real YYYY-MM-DD dates', () => {
    expect(isDay('2026-10-28')).toBe(true);
    expect(isDay('2026-02-30')).toBe(false);
    expect(isDay('2026-1-5')).toBe(false);
    expect(isDay('')).toBe(false);
  });

  it('turns a day into an inclusive UTC range', () => {
    expect(dayStart('2026-10-01')).toBe('2026-10-01T00:00:00Z');
    expect(dayEnd('2026-10-27')).toBe('2026-10-27T23:59:59Z');
  });
});
