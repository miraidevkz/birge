// Typed access to tokens.js for the few places a class name cannot be used:
// props that take a color value (ActivityIndicator, TextInput placeholder,
// navigation theme) and animation durations.
import { useColorScheme } from 'react-native';

import tokens from '../tokens';

export { tokens };

/** ColorName is one semantic color token. */
export type ColorName = keyof typeof tokens.colors.light;

/** ThemeColors maps every color token to its hex value in one scheme. */
export type ThemeColors = Record<ColorName, string>;

/** useThemeColors returns the color tokens of the current OS color scheme. */
export function useThemeColors(): ThemeColors {
  return useColorScheme() === 'dark' ? tokens.colors.dark : tokens.colors.light;
}
