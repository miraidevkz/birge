// Test environment shared by every workspace.

// Safe-area insets come from native code; the library ships a mock for Jest.
jest.mock('react-native-safe-area-context', () => require('react-native-safe-area-context/jest/mock').default);

// Keyboard handling is native as well; the library's Jest mock renders plain views.
jest.mock('react-native-keyboard-controller', () => require('react-native-keyboard-controller/jest'));

// Tests assert on English strings. Initialise i18n once, synchronously, before
// any component renders, exactly as the app does at startup.
require('@birge/i18n').initI18n(['en']);
