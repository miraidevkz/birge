// Font files for the families named in tokens.js. The app loads them once at
// startup with expo-font's useFonts(fonts). Each weight is imported from its
// own entry point so only these five files are bundled, not every weight.
import { NotoSans_400Regular } from '@expo-google-fonts/noto-sans/400Regular';
import { NotoSans_600SemiBold } from '@expo-google-fonts/noto-sans/600SemiBold';
import { NotoSans_700Bold } from '@expo-google-fonts/noto-sans/700Bold';
import { Nunito_700Bold } from '@expo-google-fonts/nunito/700Bold';
import { Nunito_800ExtraBold } from '@expo-google-fonts/nunito/800ExtraBold';

import { tokens } from './theme';

/** fonts maps each token font family name to its bundled file. */
export const fonts = {
  [tokens.fontFamily.regular]: NotoSans_400Regular,
  [tokens.fontFamily.semibold]: NotoSans_600SemiBold,
  [tokens.fontFamily.bold]: NotoSans_700Bold,
  [tokens.fontFamily.heading]: Nunito_700Bold,
  [tokens.fontFamily.display]: Nunito_800ExtraBold,
};
