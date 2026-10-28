// The app's API and sign-in clients, configured once.
import { createApiClient, createKratosClient } from '@birge/api';

import { config } from './config';
import { newIdempotencyKey, sha256Hex } from './platform-crypto';
import { getSessionToken } from './session';

/** api is the Birge API client. */
export const api = createApiClient({
  baseUrl: config.apiUrl,
  appVersion: config.appVersion,
  platform: config.platform,
  session: getSessionToken,
  newIdempotencyKey,
});

/** kratos is the member sign-in client (through the auth gateway). */
export const kratos = createKratosClient({ baseUrl: config.kratosUrl, sha256Hex });

/**
 * publicApi is the client for operations with `security: []` that must never
 * carry a member session, today only viewExternalPlan. The page it serves is
 * opened by an external contact, but the browser may still hold a member's
 * session (a shared or family device); the plan link must not be tied to that
 * account, so this client never sets Authorization.
 */
export const publicApi = createApiClient({
  baseUrl: config.apiUrl,
  appVersion: config.appVersion,
  platform: config.platform,
  session: () => null,
  newIdempotencyKey,
});
