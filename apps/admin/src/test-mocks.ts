// Module factories for jest.mock in admin tests. Jest hoists jest.mock calls
// above imports, so factories require this file instead of closing over
// imported values. Synthetic data only.

/** expoConstants mocks expo-constants with the app.config `extra` tests run with. */
export function expoConstants() {
  return {
    __esModule: true,
    default: {
      expoConfig: {
        version: '0.1.0',
        extra: {
          apiUrl: 'https://api.example.invalid/v1',
          staffIssuer: 'https://idp.example.invalid/',
          staffClientId: 'birge-admin',
        },
      },
    },
  };
}

/** platformCrypto mocks @/lib/platform-crypto with deterministic keys. */
export function platformCrypto() {
  let n = 0;
  return {
    newIdempotencyKey: () => `01900000-0000-7000-8000-${String(++n).padStart(12, '0')}`,
    randomBytes: (len: number) => new Uint8Array(len),
  };
}
