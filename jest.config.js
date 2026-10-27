// One Jest run for every client workspace, so `npm test` covers apps and
// packages with the same React Native environment (jest-expo). Each app gets
// its own project so the `@/` alias resolves to that app's `src/`.
const shared = {
  preset: 'jest-expo',
  testPathIgnorePatterns: ['/node_modules/', '/dist/'],
  // jest-expo's list plus the ESM-only packages we use (they ship untranspiled code).
  transformIgnorePatterns: [
    '/node_modules/(?!(.pnpm|react-native|@react-native|@react-native-community|expo|@expo|@expo-google-fonts|react-navigation|@react-navigation|@shopify/flash-list|nativewind|react-native-css-interop|openapi-fetch|expo-auth-session|expo-web-browser))',
    '/node_modules/react-native-reanimated/plugin/',
    '/node_modules/@react-native/babel-preset/',
  ],
  // Reanimated 4's own resolver: picks its web implementation for the parts
  // that need the native worklets runtime, which Jest does not have.
  resolver: 'react-native-reanimated/jest/resolver',
  setupFilesAfterEnv: ['<rootDir>/jest.setup.js'],
};

module.exports = {
  projects: [
    {
      ...shared,
      displayName: 'mobile',
      testMatch: ['<rootDir>/apps/mobile/**/*.test.[jt]s?(x)'],
      moduleNameMapper: {
        '^@/(.*)$': '<rootDir>/apps/mobile/src/$1',
      },
    },
    {
      ...shared,
      displayName: 'admin',
      testMatch: ['<rootDir>/apps/admin/**/*.test.[jt]s?(x)'],
      moduleNameMapper: {
        '^@/(.*)$': '<rootDir>/apps/admin/src/$1',
      },
    },
    {
      ...shared,
      displayName: 'packages',
      testMatch: ['<rootDir>/packages/**/*.test.[jt]s?(x)'],
    },
  ],
};
