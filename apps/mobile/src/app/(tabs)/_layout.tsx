// Tabs for a signed-in member. Waits for the stored session before choosing,
// so a cold start never flashes the sign-in screen. The bar is full (four
// peers), so the inbox has no tab of its own: Home shows the latest items and
// the way to all of them, and the Home tab carries the unread badge.
import { useTranslation } from '@birge/i18n';
import { tokens, useTabBarOptions, useThemeColors } from '@birge/ui';
import Ionicons from '@expo/vector-icons/Ionicons';
import { Redirect, Tabs } from 'expo-router';
import type { ComponentProps } from 'react';

import { badgeText, useUnreadCount } from '@/lib/inbox';
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
  // Phones: a bottom bar. Tablets and desktops: a side rail (shared with the admin).
  const tabBarOptions = useTabBarOptions();
  const colors = useThemeColors();
  const { hydrated, signedIn } = useSession();
  const { t: tInbox } = useTranslation('inbox');
  const unread = useUnreadCount(hydrated && signedIn);
  if (!hydrated) return null;
  if (!signedIn) return <Redirect href="/sign-in" />;
  return (
    <Tabs screenOptions={tabBarOptions}>
      <Tabs.Screen
        name="index"
        options={{
          title: t('tabs.home'),
          tabBarIcon: icon('home-outline'),
          tabBarBadge: badgeText(unread),
          tabBarBadgeStyle: {
            backgroundColor: colors.primary,
            color: colors.primaryForeground,
            fontFamily: tokens.fontFamily.semibold,
          },
          tabBarAccessibilityLabel:
            unread > 0 ? `${t('tabs.home')}, ${tInbox('badgeLabel', { n: unread })}` : t('tabs.home'),
        }}
      />
      <Tabs.Screen name="circle" options={{ title: t('tabs.circle'), tabBarIcon: icon('people-outline') }} />
      <Tabs.Screen name="help" options={{ title: t('tabs.help'), tabBarIcon: icon('help-buoy-outline') }} />
      <Tabs.Screen name="profile" options={{ title: t('tabs.profile'), tabBarIcon: icon('person-outline') }} />
    </Tabs>
  );
}
