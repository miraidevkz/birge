// Staff OIDC access token: authorization code + PKCE against the configured
// issuer (ADR-0008). The token lives in process memory only — never
// localStorage or SecureStore — so a reload requires sign-in again. Nothing
// here logs the token, contact details or notes.
import {
  AuthRequest,
  exchangeCodeAsync,
  fetchDiscoveryAsync,
  makeRedirectUri,
  ResponseType,
  type DiscoveryDocument,
} from 'expo-auth-session';
import * as WebBrowser from 'expo-web-browser';

import { config } from './config';

WebBrowser.maybeCompleteAuthSession();

/** StaffProfile is what the IdP userinfo (or id_token claims) gave us. */
export interface StaffProfile {
  subject: string;
  roles: string[];
}

/** AuthSnapshot is the in-memory signed-in state. */
export interface AuthSnapshot {
  accessToken: string;
  profile: StaffProfile;
}

let current: AuthSnapshot | null = null;
let expireHandler: (() => void) | null = null;
let mfaHandler: (() => void) | null = null;

/** getAccessToken returns the bearer token for /v1/staff/*, or null. */
export function getAccessToken(): string | null {
  return current?.accessToken ?? null;
}

/** getStaffProfile returns the signed-in profile, or null. */
export function getStaffProfile(): StaffProfile | null {
  return current?.profile ?? null;
}

/** setAccessToken stores the token in memory only (tests and the session provider). */
export function setAccessToken(token: string | null, profile?: StaffProfile | null): void {
  if (token === null) {
    current = null;
    return;
  }
  current = {
    accessToken: token,
    profile: profile ?? { subject: '', roles: [] },
  };
}

/** clearAccessToken drops the in-memory token. */
export function clearAccessToken(): void {
  current = null;
}

/** expireAccessToken is called on a 401 from the API. */
export function expireAccessToken(): void {
  expireHandler?.();
}

/** requireMfa is called on a 403 with code mfa_required. */
export function requireMfa(): void {
  mfaHandler?.();
}

/** AuthHandlers wire the React session provider to the module-level token. */
export interface AuthHandlers {
  onExpire: () => void;
  onMfaRequired: () => void;
}

/** registerAuthHandlers lets the session provider clear UI state on 401 / MFA. */
export function registerAuthHandlers(handlers: AuthHandlers | null): void {
  expireHandler = handlers?.onExpire ?? null;
  mfaHandler = handlers?.onMfaRequired ?? null;
}

/** rolesFromClaims extracts staff roles from an IdP userinfo / claims object. */
export function rolesFromClaims(claims: Record<string, unknown>): string[] {
  const direct = claims.roles;
  if (Array.isArray(direct)) return direct.filter((r): r is string => typeof r === 'string');
  const realm = claims.realm_access;
  if (realm && typeof realm === 'object' && !Array.isArray(realm)) {
    const nested = (realm as Record<string, unknown>).roles;
    if (Array.isArray(nested)) return nested.filter((r): r is string => typeof r === 'string');
  }
  const ext = claims.ext;
  if (ext && typeof ext === 'object' && !Array.isArray(ext)) {
    const nested = (ext as Record<string, unknown>).roles;
    if (Array.isArray(nested)) return nested.filter((r): r is string => typeof r === 'string');
  }
  return [];
}

/** profileFromClaims builds a StaffProfile from userinfo / token claims. */
export function profileFromClaims(claims: Record<string, unknown>): StaffProfile {
  const subject = typeof claims.sub === 'string' ? claims.sub : '';
  return { subject, roles: rolesFromClaims(claims) };
}

async function loadUserinfo(discovery: DiscoveryDocument, accessToken: string): Promise<StaffProfile> {
  const endpoint = discovery.userInfoEndpoint;
  if (!endpoint) return { subject: '', roles: [] };
  const res = await fetch(endpoint, {
    headers: { Authorization: `Bearer ${accessToken}` },
  });
  if (!res.ok) return { subject: '', roles: [] };
  const body: unknown = await res.json().catch(() => null);
  if (!body || typeof body !== 'object' || Array.isArray(body)) return { subject: '', roles: [] };
  return profileFromClaims(body as Record<string, unknown>);
}

/**
 * signInWithOidc runs the authorization-code + PKCE flow against the staff
 * issuer discovery document and keeps the access token in memory only.
 */
export async function signInWithOidc(): Promise<AuthSnapshot> {
  const discovery = await fetchDiscoveryAsync(config.staffIssuer);
  // On web makeRedirectUri() is the bare origin, which is the member app;
  // the redirect has to come back to the admin's own path.
  const redirectUri = makeRedirectUri({ path: config.basePath.replace(/^\//, '') });
  const request = new AuthRequest({
    clientId: config.staffClientId,
    scopes: ['openid', 'offline_access'],
    redirectUri,
    usePKCE: true,
    responseType: ResponseType.Code,
  });
  await request.makeAuthUrlAsync(discovery);
  const result = await request.promptAsync(discovery);
  if (result.type !== 'success' || !result.params.code) {
    throw new Error('sign-in cancelled');
  }
  const codeVerifier = request.codeVerifier;
  if (!codeVerifier) throw new Error('sign-in failed: missing PKCE verifier');
  const token = await exchangeCodeAsync(
    {
      clientId: config.staffClientId,
      code: result.params.code,
      redirectUri,
      extraParams: { code_verifier: codeVerifier },
    },
    discovery,
  );
  if (!token.accessToken) throw new Error('sign-in failed: no access token');
  const profile = await loadUserinfo(discovery, token.accessToken);
  current = { accessToken: token.accessToken, profile };
  return current;
}

/** signOutLocally clears the in-memory token (no provider revoke in MVP). */
export function signOutLocally(): void {
  current = null;
}
