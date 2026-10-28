// Staff: everyone the deployment has seen sign in, most recently active first
// (listStaffAccounts), admin only. The IdP stays the authority on who is
// staff; this list exists to read the audit log. Tapping a person opens the
// staff card. Names stay on screen only: never logged, never in a query key.
import { useTranslation } from '@birge/i18n';
import { List, ListItem, Screen } from '@birge/ui';
import { router } from 'expo-router';

import { isForbidden, isNotImplemented } from '@/lib/not-implemented';
import { staffName, useStaffAccounts, type StaffAccount } from '@/lib/staff';
import { formatDateTime } from '@/lib/time';

/** StaffListScreen lists staff accounts and opens a staff card. */
export function StaffListScreen() {
  const { t } = useTranslation('admin');
  const accounts = useStaffAccounts();
  const notAvailable = isNotImplemented(accounts.error);
  const forbidden = isForbidden(accounts.error);
  const calm = notAvailable || forbidden;

  const emptyState = () => {
    if (forbidden) return { title: t('audit.forbiddenTitle'), description: t('audit.forbiddenDescription') };
    if (notAvailable) return { title: t('notAvailable.title'), description: t('notAvailable.description') };
    return { title: t('staff.emptyTitle'), description: t('staff.emptyDescription') };
  };

  return (
    <Screen title={t('staff.title')} scroll={false}>
      <List<StaffAccount>
        data={calm ? [] : accounts.data?.items}
        loading={accounts.isLoading}
        error={calm ? undefined : accounts.error}
        onRetry={() => void accounts.refetch()}
        keyExtractor={(a) => a.id}
        empty={emptyState()}
        testID="staff"
        renderItem={({ item }) => {
          const roles = item.roles.map((r) => t(`audit.role.${r}`)).join(', ');
          const seen = t('staff.lastSeen', { time: formatDateTime(item.lastSeenAt) });
          return (
            <ListItem
              title={staffName(item.id, item.displayName)}
              subtitle={roles ? `${roles} · ${seen}` : seen}
              onPress={() => router.push({ pathname: '/audit/staff/[staffId]', params: { staffId: item.id } })}
              testID={`staff-${item.id}`}
            />
          );
        }}
      />
    </Screen>
  );
}
