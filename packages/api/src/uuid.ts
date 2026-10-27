// UUIDv7 (RFC 9562) for Idempotency-Key. The contract asks for v7: keys sort
// by creation time, which keeps the server's idempotency index compact.
//
// This is ID formatting, not cryptography: the 74 random bits come from the
// platform's secure random source, passed in by the caller (expo-crypto in the
// app, node:crypto in tests).

/** RandomBytes returns n bytes from a cryptographically secure source. */
export type RandomBytes = (n: number) => Uint8Array;

/** uuidv7 returns a version 7 UUID for the given Unix time in milliseconds. */
export function uuidv7(randomBytes: RandomBytes, nowMs: number = Date.now()): string {
  const b = Uint8Array.from(randomBytes(16));
  if (b.length !== 16) throw new Error('uuidv7: randomBytes must return 16 bytes');
  // 48-bit big-endian timestamp. Division instead of bit shifts: JS bitwise
  // operators truncate to 32 bits.
  let ts = Math.floor(nowMs);
  for (let i = 5; i >= 0; i--) {
    b[i] = ts % 256;
    ts = Math.floor(ts / 256);
  }
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x70; // version 7
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80; // variant 10
  const hex = Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
