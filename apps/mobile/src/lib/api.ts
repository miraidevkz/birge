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
