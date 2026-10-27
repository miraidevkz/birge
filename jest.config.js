// One Jest run for every client workspace, so `npm test` covers apps and
// packages with the same React Native environment (jest-expo).
module.exports = {
  preset: 'jest-expo',
  roots: ['<rootDir>/apps', '<rootDir>/packages'],
  testPathIgnorePatterns: ['/node_modules/', '/dist/'],
  // jest-expo's list plus the ESM-only packages we use (they ship untranspiled code).
  transformIgnorePatterns: [
    '/node_modules/(?!(.pnpm|react-native|@react-native|@react-native-community|expo|@expo|@expo-google-fonts|react-navigation|@react-navigation|@shopify/flash-list|nativewind|react-native-css-interop|openapi-fetch))',
    '/node_modules/react-native-reanimated/plugin/',
    '/node_modules/@react-native/babel-preset/',
  ],
  // Reanimated 4's own resolver: picks its web implementation for the parts
  // that need the native worklets runtime, which Jest does not have.
  resolver: 'react-native-reanimated/jest/resolver',
  moduleNameMapper: {
    // Same alias as apps/mobile/tsconfig.json.
    '^@/(.*)$': '<rootDir>/apps/mobile/src/$1',
  },
  setupFilesAfterEnv: ['<rootDir>/jest.setup.js'],
};
