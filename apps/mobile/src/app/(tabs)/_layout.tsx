// Tabs for a signed-in member. Waits for the stored session before choosing,
// so a cold start never flashes the sign-in screen.
import { useTranslation } from '@birge/i18n';
import { tokens, useThemeColors } from '@birge/ui';
import Ionicons from '@expo/vector-icons/Ionicons';
import { Redirect, Tabs } from 'expo-router';
import type { ComponentProps } from 'react';

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
        tabBarStyle: { backgroundColor: colors.card, borderTopColor: colors.separator },
        tabBarLabelStyle: { fontFamily: tokens.fontFamily.semibold },
      }}
    >
      <Tabs.Screen name="index" options={{ title: t('tabs.home'), tabBarIcon: icon('home-outline') }} />
      <Tabs.Screen name="circle" options={{ title: t('tabs.circle'), tabBarIcon: icon('people-outline') }} />
      <Tabs.Screen name="help" options={{ title: t('tabs.help'), tabBarIcon: icon('help-buoy-outline') }} />
      <Tabs.Screen name="profile" options={{ title: t('tabs.profile'), tabBarIcon: icon('person-outline') }} />
    </Tabs>
  );
}
