// Staff card (getStaffAccount), admin only: name, roles, first and last seen,
// and how many times the person did each action in the last 30 days. "Show
// their actions" returns to Audit with the person filter set. A 404 with code
// not_found means there is no such staff account; an unmounted route or a 501
// means the server does not serve cards yet (lib/not-implemented).
import { ApiError } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Button, Card, List, ListItem, Screen, Text } from '@birge/ui';
import { router, useLocalSearchParams } from 'expo-router';

import { auditLabelKey } from '@/lib/audit-codes';
import { isForbidden, isNotImplemented } from '@/lib/not-implemented';
import { staffName, useStaffAccount } from '@/lib/staff';
import { formatDateTime } from '@/lib/time';

type ActionCount = { action: string; count: number };

/** StaffCardScreen shows one staff member's card and links to their audit entries. */
export function StaffCardScreen() {
  const { t } = useTranslation('admin');
  const params = useLocalSearchParams<{ staffId?: string }>();
  const staffId = typeof params.staffId === 'string' ? params.staffId : '';
  const card = useStaffAccount(staffId);

  const notAvailable = isNotImplemented(card.error);
  const forbidden = isForbidden(card.error);
  const notFound = card.error instanceof ApiError && card.error.status === 404 && !notAvailable;
  const calm = notAvailable || forbidden || notFound;

  const emptyState = () => {
    if (forbidden) return { title: t('audit.forbiddenTitle'), description: t('audit.forbiddenDescription') };
    if (notAvailable) return { title: t('notAvailable.title'), description: t('notAvailable.description') };
    if (notFound) return { title: t('staff.notFoundTitle'), description: t('staff.notFoundDescription') };
    return { title: t('staff.noActionsTitle'), description: t('staff.noActionsDescription') };
  };

  const account = card.data?.account;
  const name = account ? staffName(account.id, account.displayName) : '';

  return (
    <Screen title={account ? name : t('staff.cardTitle')} scroll={false}>
      <List<ActionCount>
        data={calm ? [] : card.data?.actions30d}
        loading={card.isLoading}
        error={calm ? undefined : card.error}
        onRetry={() => void card.refetch()}
        keyExtractor={(a) => a.action}
        empty={emptyState()}
        testID="staff-card"
        header={
          account ? (
            <>
              <Card testID="staff-card-account">
                <Text variant="label">{t('staff.roles')}</Text>
                <Text>
                  {account.roles.length > 0
                    ? account.roles.map((r) => t(`audit.role.${r}`)).join(', ')
                    : t('staff.noRoles')}
                </Text>
                <Text tone="muted">{t('staff.firstSeen', { time: formatDateTime(account.firstSeenAt) })}</Text>
                <Text tone="muted">{t('staff.lastSeen', { time: formatDateTime(account.lastSeenAt) })}</Text>
              </Card>
              <Button
                title={t('staff.showActions')}
                variant="secondary"
                onPress={() => router.dismissTo({ pathname: '/audit', params: { actorId: account.id } })}
                testID="staff-show-actions"
              />
              <Text variant="label">{t('staff.actions30d')}</Text>
            </>
          ) : undefined
        }
        renderItem={({ item }) => {
          const key = auditLabelKey('action', item.action);
          return (
            <ListItem
              title={key ? t(key) : t('audit.unknown.action')}
              subtitle={key ? undefined : item.action}
              trailing={<Text variant="label">{String(item.count)}</Text>}
              testID={`staff-action-${item.action}`}
            />
          );
        }}
      />
    </Screen>
  );
}
