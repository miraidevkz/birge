// The Birge API client: openapi-fetch over the types generated from
// contracts/openapi/birge.yaml (ADR-0005). There are no hand-written request
// or response types; if a call does not typecheck, the contract is the place
// to change.
//
// Headers that every request needs are set here once, so screens cannot forget
// them:
//   - Authorization: the member session token (ADR-0008), when signed in;
//   - X-App-Version, X-App-Platform: the server counts requests per version to
//     decide when an old version can be retired (ADR-0018);
//   - Idempotency-Key on every mutation, so a retry after a lost answer has
//     one effect. A caller that replays a stored request passes its original
//     key and it is kept.
import createClient, { type Client, type Middleware } from 'openapi-fetch';

import { NetworkError, toApiError } from './errors';
import type { paths } from './schema.gen';

/** InjectedHeader names the headers the client sets itself. */
export type InjectedHeader = 'Authorization' | 'Idempotency-Key' | 'X-App-Version' | 'X-App-Platform';

// The contract declares Idempotency-Key and the app headers as required
// parameters of each operation. The middleware fills them, so the types
// below remove them from what callers must pass. Everything else about each
// operation is the generated type unchanged.
type Method = 'get' | 'put' | 'post' | 'delete' | 'patch' | 'options' | 'head' | 'trace';
type StripHeaderParams<P> = P extends { header?: infer H }
  ? [NonNullable<H>] extends [never]
    ? P
    : [Exclude<keyof NonNullable<H>, InjectedHeader>] extends [never]
      ? Omit<P, 'header'> & { header?: never }
      : Omit<P, 'header'> & { header: Omit<NonNullable<H>, InjectedHeader> }
  : P;
type StripOperation<O> = O extends { parameters: infer P }
  ? Omit<O, 'parameters'> & { parameters: StripHeaderParams<P> }
  : O;

/** ApiPaths is the generated `paths` with client-injected headers removed from the parameters. */
export type ApiPaths = {
  [Path in keyof paths]: {
    [M in keyof paths[Path]]: M extends Method ? StripOperation<paths[Path][M]> : paths[Path][M];
  };
};

/** ApiClient is the typed client; call it as `client.GET('/me/inbox', { params: { query: { limit: 5 } } })`. */
export type ApiClient = Client<ApiPaths>;

/** AppPlatform is the X-App-Platform value. */
export type AppPlatform = 'ios' | 'android' | 'web';

/** SessionSource returns the current member session token, or null when signed out. */
export type SessionSource = () => string | null | Promise<string | null>;

/** ApiClientConfig configures createApiClient. */
export interface ApiClientConfig {
  /** baseUrl is the API root including the version prefix, e.g. "https://api.example.invalid/v1". */
  baseUrl: string;
  /** appVersion is the semantic version of the running app ("1.4.0"). */
  appVersion: string;
  platform: AppPlatform;
  session: SessionSource;
  /** newIdempotencyKey returns a fresh UUIDv7 (see uuidv7). */
  newIdempotencyKey: () => string;
  /** fetch overrides the global fetch, for tests. */
  fetch?: typeof fetch;
}

const MUTATING = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

/** createApiClient builds the client with the header middleware installed. */
export function createApiClient(config: ApiClientConfig): ApiClient {
  const client = createClient<ApiPaths>({
    baseUrl: config.baseUrl.replace(/\/+$/, ''),
    ...(config.fetch ? { fetch: config.fetch } : {}),
  });
  const headers: Middleware = {
    async onRequest({ request }) {
      request.headers.set('X-App-Version', config.appVersion);
      request.headers.set('X-App-Platform', config.platform);
      const token = await config.session();
      if (token) request.headers.set('Authorization', `Bearer ${token}`);
      if (MUTATING.has(request.method) && !request.headers.has('Idempotency-Key')) {
        request.headers.set('Idempotency-Key', config.newIdempotencyKey());
      }
      return request;
    },
  };
  client.use(headers);
  return client;
}

/** FetchResult is the shape openapi-fetch resolves to, reduced to what unwrap needs. */
export interface FetchResult<D> {
  data?: D;
  error?: unknown;
  response: Response;
}

/**
 * unwrap resolves to the response data of a successful call and throws
 * ApiError (HTTP error, problem+json mapped) or NetworkError (no answer).
 * Screens and React Query use it so that failures are thrown, not returned.
 */
export async function unwrap<D>(call: Promise<FetchResult<D>>): Promise<D> {
  let result: FetchResult<D>;
  try {
    result = await call;
  } catch (cause) {
    throw new NetworkError(cause);
  }
  if (!result.response.ok) throw toApiError(result.response.status, result.error);
  return result.data as D;
}
