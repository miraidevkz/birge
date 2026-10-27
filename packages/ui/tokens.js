// @ts-check
// Design tokens: the single source of every visual value in the clients
// (ADR-0014). The Tailwind preset (tailwind-preset.js) turns them into
// NativeWind classes; components that need a raw value (an ActivityIndicator
// color, a Modal animation) read them through src/theme.ts. Never copy a
// value from here into a component.
//
// CommonJS on purpose: Tailwind loads the preset with Node's require.
//
// Colors: every foreground/background pair used for text meets WCAG 2.2 AA
// (4.5:1) in both schemes, and `border` meets 3:1 against surfaces for input
// outlines (1.4.11). src/theme.test.ts enforces this; adjust a value there
// first if a check fails, never relax the test.

const colors = {
  light: {
    background: '#FBF8F4',
    foreground: '#1F1A17',
    card: '#FFFFFF',
    cardForeground: '#1F1A17',
    muted: '#F1ECE6',
    mutedForeground: '#5C534C',
    border: '#8C8178',
    separator: '#E6DFD7',
    primary: '#2F6B5E',
    primaryForeground: '#FFFFFF',
    secondary: '#E3ECE9',
    secondaryForeground: '#1E4A40',
    destructive: '#B3261E',
    destructiveForeground: '#FFFFFF',
    infoBg: '#E6F0F7',
    infoFg: '#0F4C75',
    successBg: '#E5F2EA',
    successFg: '#1D5E3A',
    warningBg: '#FBF0DC',
    warningFg: '#7A4B00',
    dangerBg: '#FBE6E4',
    dangerFg: '#8C1D18',
  },
  dark: {
    background: '#14110F',
    foreground: '#F3EEE9',
    card: '#1C1815',
    cardForeground: '#F3EEE9',
    muted: '#241F1B',
    mutedForeground: '#B8ADA3',
    border: '#7A6F66',
    separator: '#2C2622',
    primary: '#7CC3B1',
    primaryForeground: '#0E2A24',
    secondary: '#23332F',
    secondaryForeground: '#CDE6DF',
    destructive: '#F2A49E',
    destructiveForeground: '#3B0B08',
    infoBg: '#10283A',
    infoFg: '#B9DCF5',
    successBg: '#12291C',
    successFg: '#B4E2C5',
    warningBg: '#33260C',
    warningFg: '#F5D597',
    dangerBg: '#3A1412',
    dangerFg: '#F7C1BC',
  },
};

/** Spacing scale in px on a 4-point grid; named by size, not by use. */
const spacing = { 0: 0, 1: 4, 2: 8, 3: 12, 4: 16, 5: 20, 6: 24, 8: 32, 10: 40, 12: 48 };

/** Corner radii in px. */
const radius = { sm: 8, md: 12, lg: 16, full: 9999 };

/**
 * Font families, one per weight: with bundled static fonts, React Native picks
 * the weight by family name, and `fontWeight` would make iOS fake a bold.
 * Noto Sans covers Kazakh Cyrillic (Әә Ғғ Ққ Ңң Өө Ұұ Үү Һһ Іі), OFL-1.1.
 */
const fontFamily = {
  regular: 'NotoSans_400Regular',
  semibold: 'NotoSans_600SemiBold',
  bold: 'NotoSans_700Bold',
};

/** Type ramp: size and line height in px. Text scales with the OS setting on top of these. */
const fontSize = {
  caption: [13, 18],
  body: [16, 24],
  label: [16, 22],
  title: [20, 28],
  display: [26, 34],
};

/** Minimum touch target in px (WCAG 2.5.8 asks 24, platform guidelines 44-48). */
const touchTarget = 48;

/**
 * Motion durations in ms (ADR-0014: 150-250 ms, no spring). Components use
 * them only when motion is allowed; with reduce motion on, or on crisis
 * screens, duration is 0 and nothing moves.
 */
const motion = { fast: 150, base: 200, slow: 250 };

module.exports = { colors, spacing, radius, fontFamily, fontSize, touchTarget, motion };
