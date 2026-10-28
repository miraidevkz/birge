// Inbox: every notification of the member, newest first, with cursor paging
// ("Show more"). Opening an item marks it read and goes to its place in the
// app where it has one (lib/notification-targets.ts). Refetched when the
// screen comes back into focus and when the app returns to the foreground.
import { useTranslation } from '@birge/i18n';
import { List, Screen } from '@birge/ui';
import { useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useCallback } from 'react';

import { openNotification, useInbox, useRefreshInboxOnFocus, type Notification } from '@/lib/inbox';

import { NotificationRow } from './notification-row';

/** InboxScreen lists the member's notifications. */
export function InboxScreen() {
  const { t } = useTranslation('inbox');
  const client = useQueryClient();
  const inbox = useInbox();
  useRefreshInboxOnFocus();
  const open = useCallback((n: Notification) => void openNotification(client, n), [client]);
  const items = inbox.data?.pages.flatMap((p) => p.items);

  return (
    <Screen title={t('title')} scroll={false}>
      <List<Notification>
        data={items}
        loading={inbox.isLoading}
        error={inbox.error}
        onRetry={() => void inbox.refetch()}
        keyExtractor={(n) => n.id}
        renderItem={({ item }) => <NotificationRow item={item} onOpen={open} />}
        empty={{
          title: t('emptyTitle'),
          description: t('emptyDescription'),
          action: { label: t('emptyAction'), onPress: () => router.push('/circle') },
        }}
        onLoadMore={inbox.hasNextPage ? () => void inbox.fetchNextPage() : undefined}
        loadingMore={inbox.isFetchingNextPage}
        testID="inbox-list"
      />
    </Screen>
  );
}
