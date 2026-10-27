// Test environment shared by every workspace.

// Safe-area insets come from native code; the library ships a mock for Jest.
jest.mock('react-native-safe-area-context', () => require('react-native-safe-area-context/jest/mock').default);

// Tests assert on English strings. Initialise i18n once, synchronously, before
// any component renders, exactly as the app does at startup.
require('@birge/i18n').initI18n(['en']);
