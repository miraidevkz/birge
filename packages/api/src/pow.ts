// Proof-of-work for sign-in (ALTCHA format), required by the auth gateway on
// every login or registration submission (internal/domains/membership/authgw).
// It makes each SMS request cost the client CPU time, which limits SMS
// pumping from scripted clients.
//
// The challenge is challenge = SHA-256(salt + number) for a secret number in
// [0, maxnumber]; the client searches for it. Hashing goes through the
// platform's digest (expo-crypto in the app), injected as sha256Hex: this file
// only searches and encodes, it implements no cryptography.

/** PowChallenge is the JSON returned by GET <auth>/pow. */
export interface PowChallenge {
  algorithm: string;
  challenge: string;
  maxnumber: number;
  salt: string;
  signature: string;
}

/** Sha256Hex returns the lowercase hex SHA-256 of a UTF-8 string. */
export type Sha256Hex = (input: string) => Promise<string>;

/** PowError means the challenge cannot be solved (unknown algorithm, or no number matched). */
export class PowError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PowError';
  }
}

/** SolveOptions tunes solvePow. */
export interface SolveOptions {
  /** batch is how many hashes run concurrently; native digests are async, so batching hides call overhead. */
  batch?: number;
  signal?: AbortSignal;
}

/**
 * solvePow finds the number and returns the base64 payload for the
 * X-Pow-Solution header. The payload echoes the server's signed fields; the
 * server checks the signature, the hash and the expiry, and accepts it once.
 */
export async function solvePow(c: PowChallenge, sha256Hex: Sha256Hex, options: SolveOptions = {}): Promise<string> {
  if (c.algorithm !== 'SHA-256') throw new PowError(`unsupported algorithm ${c.algorithm}`);
  if (!Number.isSafeInteger(c.maxnumber) || c.maxnumber < 0) throw new PowError('invalid maxnumber');
  const batch = Math.max(1, options.batch ?? 64);
  const want = c.challenge.toLowerCase();
  for (let start = 0; start <= c.maxnumber; start += batch) {
    if (options.signal?.aborted) throw new PowError('aborted');
    const end = Math.min(start + batch - 1, c.maxnumber);
    const numbers: number[] = [];
    for (let n = start; n <= end; n++) numbers.push(n);
    const hashes = await Promise.all(numbers.map((n) => sha256Hex(c.salt + String(n))));
    const hit = hashes.findIndex((h) => h.toLowerCase() === want);
    if (hit >= 0) {
      const payload = {
        algorithm: c.algorithm,
        challenge: c.challenge,
        number: numbers[hit],
        salt: c.salt,
        signature: c.signature,
      };
      // The JSON is ASCII (hex, digits, "?expires="), so btoa is safe here.
      return btoa(JSON.stringify(payload));
    }
  }
  throw new PowError('no solution');
}
