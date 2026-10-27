import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { ApiError } from '@birge/api';

import {
  clearAccessToken,
  expireAccessToken,
  getAccessToken,
  profileFromClaims,
  registerAuthHandlers,
  requireMfa,
  rolesFromClaims,
  setAccessToken,
  signOutLocally,
} from './auth';
import { createQueryClient } from './query';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('expo-web-browser', () => ({
  maybeCompleteAuthSession: jest.fn(),
}));
jest.mock('expo-auth-session', () => ({
  AuthRequest: jest.fn(),
  ResponseType: { Code: 'code' },
  exchangeCodeAsync: jest.fn(),
  fetchDiscoveryAsync: jest.fn(),
  makeRedirectUri: jest.fn(() => 'https://admin.example.invalid/'),
}));

describe('auth token storage', () => {
  beforeEach(() => {
    clearAccessToken();
    registerAuthHandlers(null);
  });

  it('keeps the access token in memory only', () => {
    expect(getAccessToken()).toBeNull();
    setAccessToken('tok-1', { subject: 'staff-1', roles: ['psychologist'] });
    expect(getAccessToken()).toBe('tok-1');
    // Nothing is written to localStorage / sessionStorage.
    expect(globalThis.localStorage?.getItem?.('birge.staff.token') ?? null).toBeNull();
    expect(globalThis.sessionStorage?.getItem?.('birge.staff.token') ?? null).toBeNull();
    signOutLocally();
    expect(getAccessToken()).toBeNull();
  });

  it('expireAccessToken invokes the registered handler and clears via it', () => {
    const onExpire = jest.fn(() => clearAccessToken());
    registerAuthHandlers({ onExpire, onMfaRequired: jest.fn() });
    setAccessToken('tok-2', { subject: 's', roles: [] });
    expireAccessToken();
    expect(onExpire).toHaveBeenCalled();
    expect(getAccessToken()).toBeNull();
  });

  it('requireMfa invokes the registered handler', () => {
    const onMfa = jest.fn();
    registerAuthHandlers({ onExpire: jest.fn(), onMfaRequired: onMfa });
    requireMfa();
    expect(onMfa).toHaveBeenCalled();
  });
});

describe('rolesFromClaims / profileFromClaims', () => {
  it('reads roles from common IdP shapes', () => {
    expect(rolesFromClaims({ roles: ['psychologist', 'admin'] })).toEqual(['psychologist', 'admin']);
    expect(rolesFromClaims({ realm_access: { roles: ['moderator'] } })).toEqual(['moderator']);
    expect(rolesFromClaims({ ext: { roles: ['lawyer'] } })).toEqual(['lawyer']);
    expect(rolesFromClaims({})).toEqual([]);
  });

  it('builds a profile from sub and roles', () => {
    expect(profileFromClaims({ sub: 'abc', roles: ['moderator'] })).toEqual({
      subject: 'abc',
      roles: ['moderator'],
    });
  });
});

describe('createQueryClient auth side effects', () => {
  beforeEach(() => {
    clearAccessToken();
  });

  it('clears the token on a 401 query error', async () => {
    const onExpire = jest.fn(() => clearAccessToken());
    registerAuthHandlers({ onExpire, onMfaRequired: jest.fn() });
    setAccessToken('tok-401', { subject: 's', roles: [] });
    const client = createQueryClient();
    await expect(
      client.fetchQuery({
        queryKey: ['fail'],
        queryFn: async () => {
          throw new ApiError(401, 'unauthenticated', 'gone');
        },
        retry: false,
      }),
    ).rejects.toBeInstanceOf(ApiError);
    expect(onExpire).toHaveBeenCalled();
    expect(getAccessToken()).toBeNull();
  });
});
