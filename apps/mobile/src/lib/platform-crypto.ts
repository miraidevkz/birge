// Platform randomness and hashing for the API package, which takes them as
// parameters to stay free of native modules. expo-crypto uses the OS sources
// (SecureRandom / SecRandomCopyBytes / crypto.getRandomValues on web).
import { uuidv7, type Sha256Hex } from '@birge/api';
import * as Crypto from 'expo-crypto';

/** randomBytes returns n secure random bytes. */
export const randomBytes = (n: number): Uint8Array => Crypto.getRandomBytes(n);

/** newIdempotencyKey returns a UUIDv7 for the Idempotency-Key header. */
export const newIdempotencyKey = (): string => uuidv7(randomBytes);

/** sha256Hex hashes for the sign-in proof-of-work. */
export const sha256Hex: Sha256Hex = (input) =>
  Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, input, { encoding: Crypto.CryptoEncoding.HEX });
