// The admin's Birge API client. The access token comes from the in-memory
// auth module (Authorization: Bearer on every /v1/staff/* call).
import { createApiClient } from '@birge/api';

import { getAccessToken } from './auth';
import { config } from './config';
import { newIdempotencyKey } from './platform-crypto';

/** api is the Birge API client for staff routes. */
export const api = createApiClient({
  baseUrl: config.apiUrl,
  appVersion: config.appVersion,
  platform: config.platform,
  session: getAccessToken,
  newIdempotencyKey,
});
