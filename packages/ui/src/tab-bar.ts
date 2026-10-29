import { useWindowDimensions } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { tokens, useThemeColors } from './theme';

/**
 * useTabBarOptions returns the shared tab navigator options of both apps:
 * a bottom bar on phones, a side rail from the tablet breakpoint and a rail
 * with labels beside the icons on desktops (a bottom bar across 1280 px looks
 * lost). Spread it into `<Tabs screenOptions>`; it imports nothing from the
 * router, so the shape follows expo-router's bottom tabs.
 */
export function useTabBarOptions() {
  const colors = useThemeColors();
  const insets = useSafeAreaInsets();
  const { width } = useWindowDimensions();
  const rail = width >= tokens.breakpoints.tablet;
  const desktop = width >= tokens.breakpoints.desktop;
  return {
    headerShown: false,
    // Tabs are peers: switching never animates (ADR-0014, expo-animation).
    animation: 'none',
    tabBarActiveTintColor: colors.primary,
    tabBarInactiveTintColor: colors.mutedForeground,
    tabBarPosition: rail ? 'left' : 'bottom',
    tabBarVariant: rail ? 'material' : 'uikit',
    tabBarLabelPosition: desktop ? 'beside-icon' : 'below-icon',
    tabBarStyle: rail
      ? {
          backgroundColor: colors.card,
          borderRightColor: colors.separator,
          paddingTop: insets.top + tokens.spacing[4],
          // The navigator gives a labelled rail a 360 px minimum (Material's
          // drawer width); ours is narrower so the content gets the room.
          ...(desktop ? { width: tokens.railWidth, minWidth: tokens.railWidth } : {}),
        }
      : {
          backgroundColor: colors.card,
          borderTopColor: colors.separator,
          // Room for the icon and a whole label line: the default 48 px bar
          // squeezed the label to 10 px and cut its lower half.
          height: tokens.touchTarget + tokens.fontSize.tab[1] + tokens.spacing[2] + insets.bottom,
          paddingTop: tokens.spacing[1],
          paddingBottom: insets.bottom + tokens.spacing[1],
        },
    // Size and line height from the ramp: without a line height the label's
    // descenders were clipped by the bar on the web.
    tabBarLabelStyle: {
      fontFamily: tokens.fontFamily.semibold,
      fontSize: tokens.fontSize.tab[0],
      lineHeight: tokens.fontSize.tab[1],
    },
  } as const;
}
