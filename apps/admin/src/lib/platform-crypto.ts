// Platform randomness for Idempotency-Key (UUIDv7). expo-crypto uses the OS
// sources (crypto.getRandomValues on web).
import { uuidv7 } from '@birge/api';
import * as Crypto from 'expo-crypto';

/** randomBytes returns n secure random bytes. */
export const randomBytes = (n: number): Uint8Array => Crypto.getRandomBytes(n);

/** newIdempotencyKey returns a UUIDv7 for the Idempotency-Key header. */
export const newIdempotencyKey = (): string => uuidv7(randomBytes);
