// Staff accounts and the audit log (admin only, every read audited by the
// server). Shared by the Audit, Staff and staff card screens. Query keys
// carry staff IDs and codes only, never a name; nothing here logs.
import { unwrap, type Schemas } from '@birge/api';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';

import { api } from './api';
import { shortId } from './audit-codes';

/** AuditEntry is one staff action: codes and IDs only. */
export type AuditEntry = Schemas['AuditEntry'];

/** StaffAccount is a staff member the deployment has seen sign in. */
export type StaffAccount = Schemas['StaffAccount'];

/** StaffRole is one staff role from the IdP. */
export type StaffRole = Schemas['StaffRole'];

/** AuditFilter narrows the audit log; every field is optional ("all"). */
export interface AuditFilter {
  actorId?: string;
  action?: string;
  objectType?: string;
  /** from is an RFC 3339 time (inclusive start). */
  from?: string;
  /** to is an RFC 3339 time (end of the range). */
  to?: string;
}

/** staffKey is the query key prefix of the staff accounts and audit reads. */
export const staffKey = ['staff', 'directory'] as const;

/**
 * useAuditEntries pages through GET /staff/audit, newest first. The server
 * audits each read, so it is not refetched on a timer.
 */
export function useAuditEntries(filter: AuditFilter) {
  return useInfiniteQuery({
    queryKey: [...staffKey, 'audit', filter],
    queryFn: ({ pageParam }) =>
      unwrap(api.GET('/staff/audit', { params: { query: { ...filter, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor,
  });
}

/** useStaffAccounts reads GET /staff/accounts (most recently active first). */
export function useStaffAccounts(enabled = true) {
  return useQuery({
    queryKey: [...staffKey, 'accounts'],
    queryFn: () => unwrap(api.GET('/staff/accounts')),
    enabled,
  });
}

/** useStaffAccount reads one staff card (GET /staff/accounts/{staffId}). */
export function useStaffAccount(staffId: string) {
  return useQuery({
    queryKey: [...staffKey, 'accounts', staffId],
    queryFn: () => unwrap(api.GET('/staff/accounts/{staffId}', { params: { path: { staffId } } })),
    enabled: staffId !== '',
  });
}

/** staffName is the name shown for a staff member: the IdP name, else a short ID. */
export function staffName(id: string, displayName?: string): string {
  return displayName && displayName.trim() !== '' ? displayName : shortId(id);
}

/** isAdmin reports whether the signed-in roles include admin (the tab bar only; the server decides). */
export function isAdmin(roles: readonly string[] | undefined): boolean {
  return roles?.includes('admin') ?? false;
}

/** AdminTab names a tab whose API the server limits to some staff roles. */
export type AdminTab =
  'escalations' | 'sos' | 'moderation' | 'members' | 'rooms' | 'duty' | 'knowledge' | 'audit' | 'metrics';

// The roles each tab's API serves, mirroring the server's checks, so a role
// never gets a tab that only answers 403. The server still decides; keep this
// in step when a service changes its roles.
const tabRoles: Record<AdminTab, readonly string[]> = {
  escalations: ['psychologist', 'moderator'],
  sos: ['moderator', 'admin'],
  moderation: ['moderator', 'admin'],
  members: ['moderator', 'admin'],
  rooms: ['moderator', 'admin'],
  duty: ['psychologist', 'moderator', 'admin'],
  knowledge: ['psychologist', 'lawyer', 'admin'],
  audit: ['admin'],
  metrics: ['admin'],
};

// Tab bar order, which is also the order firstTabHref picks from.
const tabOrder: readonly AdminTab[] = [
  'escalations',
  'sos',
  'moderation',
  'members',
  'rooms',
  'duty',
  'knowledge',
  'audit',
  'metrics',
];

/** canSeeTab reports whether any signed-in role is served by the tab's API (the tab bar only). */
export function canSeeTab(tab: AdminTab, roles: readonly string[] | undefined): boolean {
  return roles?.some((r) => tabRoles[tab].includes(r)) ?? false;
}

/**
 * firstTabHref is where a role lands when the first tab (escalations) is not
 * for it: the first tab it can see, else the profile.
 */
export function firstTabHref(roles: readonly string[] | undefined): '/' | `/${AdminTab}` | '/profile' {
  const tab = tabOrder.find((t) => canSeeTab(t, roles));
  if (tab === undefined) return '/profile';
  return tab === 'escalations' ? '/' : `/${tab}`;
}
