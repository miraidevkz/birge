// Tabs for a signed-in member. Waits for the stored session before choosing,
// so a cold start never flashes the sign-in screen.
import { useTranslation } from '@birge/i18n';
import { tokens, useThemeColors } from '@birge/ui';
import Ionicons from '@expo/vector-icons/Ionicons';
import { Redirect, Tabs } from 'expo-router';
import type { ComponentProps } from 'react';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { useSession } from '@/lib/session';

type IconName = ComponentProps<typeof Ionicons>['name'];

function icon(name: IconName) {
  // The navigator passes a ColorValue; our tint colors are always hex strings.
  return function TabIcon({ color, size }: { color: unknown; size: number }) {
    return <Ionicons name={name} color={typeof color === 'string' ? color : undefined} size={size} />;
  };
}

export default function TabsLayout() {
  const { t } = useTranslation('common');
  const colors = useThemeColors();
  const insets = useSafeAreaInsets();
  const { hydrated, signedIn } = useSession();
  if (!hydrated) return null;
  if (!signedIn) return <Redirect href="/sign-in" />;
  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        // Tabs are peers: switching never animates (ADR-0014, expo-animation).
        animation: 'none',
        tabBarActiveTintColor: colors.primary,
        tabBarInactiveTintColor: colors.mutedForeground,
        // Room for the icon and a whole label line: the default 48 px bar
        // squeezed the label to 10 px and cut its lower half.
        tabBarStyle: {
          backgroundColor: colors.card,
          borderTopColor: colors.separator,
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
      }}
    >
      <Tabs.Screen name="index" options={{ title: t('tabs.home'), tabBarIcon: icon('home-outline') }} />
      <Tabs.Screen name="circle" options={{ title: t('tabs.circle'), tabBarIcon: icon('people-outline') }} />
      <Tabs.Screen name="help" options={{ title: t('tabs.help'), tabBarIcon: icon('help-buoy-outline') }} />
      <Tabs.Screen name="profile" options={{ title: t('tabs.profile'), tabBarIcon: icon('person-outline') }} />
    </Tabs>
  );
}
