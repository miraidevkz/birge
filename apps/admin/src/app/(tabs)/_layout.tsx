// Tabs for signed-in staff. There is nothing to hydrate from storage: a missing
// in-memory token always redirects to sign-in (including after reload).
import { useTranslation } from '@birge/i18n';
import { tokens, useThemeColors } from '@birge/ui';
import Ionicons from '@expo/vector-icons/Ionicons';
import { Redirect, Tabs } from 'expo-router';
import type { ComponentProps } from 'react';

import { useSession } from '@/lib/session';
import { type AdminTab, canSeeTab } from '@/lib/staff';

type IconName = ComponentProps<typeof Ionicons>['name'];

function icon(name: IconName) {
  return function TabIcon({ color, size }: { color: unknown; size: number }) {
    return <Ionicons name={name} color={typeof color === 'string' ? color : undefined} size={size} />;
  };
}

export default function TabsLayout() {
  const { t } = useTranslation('admin');
  const colors = useThemeColors();
  const { hydrated, signedIn, mfaRequired, profile } = useSession();
  // A tab whose API does not serve any of the roles is hidden (the server still
  // returns 403); the escalations index redirects away instead.
  const href = (tab: AdminTab) => (canSeeTab(tab, profile?.roles) ? undefined : null);
  if (!hydrated) return null;
  if (!signedIn || mfaRequired) return <Redirect href="/sign-in" />;
  return (
    <Tabs
      screenOptions={{
        headerShown: false,
        animation: 'none',
        tabBarActiveTintColor: colors.primary,
        tabBarInactiveTintColor: colors.mutedForeground,
        tabBarStyle: { backgroundColor: colors.card, borderTopColor: colors.separator },
        tabBarLabelStyle: { fontFamily: tokens.fontFamily.semibold },
      }}
    >
      <Tabs.Screen
        name="index"
        options={{ title: t('tabs.escalations'), tabBarIcon: icon('alert-circle-outline'), href: href('escalations') }}
      />
      <Tabs.Screen
        name="sos"
        options={{ title: t('tabs.sos'), tabBarIcon: icon('hand-left-outline'), href: href('sos') }}
      />
      <Tabs.Screen
        name="moderation"
        options={{
          title: t('tabs.moderation'),
          tabBarIcon: icon('shield-checkmark-outline'),
          href: href('moderation'),
        }}
      />
      <Tabs.Screen
        name="members"
        options={{ title: t('tabs.members'), tabBarIcon: icon('people-outline'), href: href('members') }}
      />
      <Tabs.Screen
        name="rooms"
        options={{ title: t('tabs.rooms'), tabBarIcon: icon('chatbubbles-outline'), href: href('rooms') }}
      />
      <Tabs.Screen
        name="duty"
        options={{ title: t('tabs.duty'), tabBarIcon: icon('calendar-outline'), href: href('duty') }}
      />
      <Tabs.Screen
        name="knowledge"
        options={{ title: t('tabs.knowledge'), tabBarIcon: icon('library-outline'), href: href('knowledge') }}
      />
      <Tabs.Screen
        name="audit"
        options={{ title: t('tabs.audit'), tabBarIcon: icon('reader-outline'), href: href('audit') }}
      />
      <Tabs.Screen
        name="metrics"
        options={{ title: t('tabs.metrics'), tabBarIcon: icon('stats-chart-outline'), href: href('metrics') }}
      />
      <Tabs.Screen name="profile" options={{ title: t('tabs.profile'), tabBarIcon: icon('person-outline') }} />
    </Tabs>
  );
}
