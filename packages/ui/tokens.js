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

// Calm pastel palette: soft sage primary, muted surfaces, dusty-rose danger
// that stays clearly danger. Saturated red/blue are avoided; every pair still
// meets WCAG AA (see src/theme.test.ts).
const colors = {
  light: {
    background: '#F3F5F4',
    foreground: '#2A302D',
    card: '#FFFFFF',
    cardForeground: '#2A302D',
    muted: '#E8EEEB',
    mutedForeground: '#4F5A55',
    border: '#7A857F',
    separator: '#D8DFDB',
    primary: '#3F6F63',
    primaryForeground: '#FFFFFF',
    secondary: '#E0EAE6',
    secondaryForeground: '#274740',
    destructive: '#9A4E4E',
    destructiveForeground: '#FFFFFF',
    infoBg: '#E6ECED',
    infoFg: '#334E58',
    successBg: '#E3EDE6',
    successFg: '#27553C',
    warningBg: '#F3EBDD',
    warningFg: '#6E4A1E',
    dangerBg: '#F2E6E5',
    dangerFg: '#7A3834',
  },
  dark: {
    background: '#141816',
    foreground: '#E8EDE9',
    card: '#1A1F1C',
    cardForeground: '#E8EDE9',
    muted: '#222826',
    mutedForeground: '#A8B3AD',
    border: '#6E7A74',
    separator: '#2A302D',
    primary: '#8FBFB0',
    primaryForeground: '#0E1A16',
    secondary: '#24302C',
    secondaryForeground: '#C5DBD3',
    destructive: '#E0A8A4',
    destructiveForeground: '#2A1210',
    infoBg: '#1A262A',
    infoFg: '#B0C8D0',
    successBg: '#1A2820',
    successFg: '#A8D0B8',
    warningBg: '#2A2418',
    warningFg: '#E0C898',
    dangerBg: '#2A1A18',
    dangerFg: '#E8C0BC',
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
