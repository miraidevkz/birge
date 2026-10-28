// One inbox item as a list row, shared by the inbox screen and the Home
// preview. Title and body come from the inbox locale namespace by template
// (an unknown template gets the generic fallback, never the raw code); an
// unread item is in the strong weight with its time in the primary tone, and
// screen readers hear "Unread".
import { i18n, useTranslation } from '@birge/i18n';
import { ListItem, Text } from '@birge/ui';
import { memo, useState } from 'react';

import { formatWhen } from '@/lib/format';
import type { Notification } from '@/lib/inbox';
import { notificationKey } from '@/lib/notification-text';

/** NotificationRowProps: the item and what a tap does. */
export interface NotificationRowProps {
  item: Notification;
  onOpen: (item: Notification) => void;
}

const exists = (key: string) => i18n.exists(key, { ns: 'inbox' });

/** NotificationRow renders one inbox item. */
export const NotificationRow = memo(function NotificationRow({ item, onOpen }: NotificationRowProps) {
  const { t } = useTranslation('inbox');
  // "Today" is fixed when the row mounts: rendering stays pure.
  const [now] = useState(() => new Date());
  const key = notificationKey(item.template, exists);
  const title = t(`${key}.title`);
  const body = t(`${key}.body`);
  const when = formatWhen(item.createdAt, i18n.language, now);
  const unread = !item.readAt;
  return (
    <ListItem
      title={title}
      subtitle={body}
      emphasis={unread}
      trailing={
        <Text variant="caption" tone={unread ? 'primary' : 'muted'}>
          {when}
        </Text>
      }
      onPress={() => onOpen(item)}
      accessibilityLabel={[title, body, when, unread ? t('unread') : null].filter(Boolean).join(', ')}
      testID={`inbox-item-${item.id}`}
    />
  );
});
