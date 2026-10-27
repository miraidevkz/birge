// @birge/api: the typed Birge API client, the offline outbox and member
// sign-in. See README.md for usage.
import type { components } from './schema.gen';

export { createApiClient, unwrap } from './client';
export type { ApiClient, ApiClientConfig, ApiPaths, AppPlatform, InjectedHeader, SessionSource } from './client';
export { ApiError, NetworkError, isRetryable, toApiError } from './errors';
export { createKratosClient, E164, SignInError } from './kratos';
export type { KratosClient, KratosConfig, PendingSignIn, Session, SignInErrorCode } from './kratos';
export { createOutbox, memoryStore, retryDelayMs, sendQueued } from './outbox';
export type {
  FlushResult,
  KeyValueStore,
  Outbox,
  OutboxConfig,
  OutboxEntry,
  OutboxEvent,
  QueueableBodies,
  QueueableOperation,
} from './outbox';
export { PowError, solvePow } from './pow';
export type { PowChallenge, Sha256Hex } from './pow';
export { uuidv7 } from './uuid';
export type { RandomBytes } from './uuid';
export type { components, operations, paths } from './schema.gen';

/** Schemas is a shortcut to the generated component schemas: `Schemas['CrisisPhone']`. */
export type Schemas = components['schemas'];
