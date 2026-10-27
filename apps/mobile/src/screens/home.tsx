// Home: the next weekly check-in and the latest inbox items.
import { unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Card, Heading, List, ListItem, Screen, Text } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';

import { api } from '@/lib/api';
import { nextCheckin } from '@/lib/next-checkin';
import { notificationKey } from '@/lib/notification-text';

type Notification = Schemas['Notification'];

/** INBOX_PREVIEW is how many inbox items Home shows. */
const INBOX_PREVIEW = 5;

function formatDateTime(date: Date, lang: string, withTime: boolean): string {
  return new Intl.DateTimeFormat(lang, {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    ...(withTime ? { hour: '2-digit', minute: '2-digit' } : {}),
  }).format(date);
}

function CheckinCard() {
  const { t } = useTranslation('home');
  const schedule = useQuery({
    queryKey: ['checkins', 'schedule'],
    queryFn: () => unwrap(api.GET('/me/checkins/schedule')),
  });
  const next = nextCheckin(schedule.data, new Date());
  let body: string;
  if (schedule.data === undefined) body = schedule.isError ? t('ui:error.description') : t('ui:loading');
  else if (next) body = formatDateTime(next, i18n.language, true);
  else body = schedule.data.enabled === false ? t('checkin.off') : t('checkin.none');
  return (
    <Card testID="home-checkin">
      <Heading>{t('checkin.heading')}</Heading>
      <Text tone={next ? 'default' : 'muted'}>{body}</Text>
    </Card>
  );
}

/** HomeScreen is the first tab. */
export function HomeScreen() {
  const { t } = useTranslation('home');
  const { t: tInbox } = useTranslation('inbox');
  const inbox = useQuery({
    queryKey: ['inbox', { limit: INBOX_PREVIEW }],
    queryFn: () => unwrap(api.GET('/me/inbox', { params: { query: { limit: INBOX_PREVIEW } } })),
  });
  const exists = (key: string) => i18n.exists(key, { ns: 'inbox' });

  return (
    <Screen title={t('title')} scroll={false}>
      <List<Notification>
        data={inbox.data?.items}
        loading={inbox.isLoading}
        error={inbox.error}
        onRetry={() => void inbox.refetch()}
        keyExtractor={(n) => n.id}
        renderItem={({ item }) => (
          <ListItem
            title={tInbox(notificationKey(item.template, exists))}
            subtitle={formatDateTime(new Date(item.createdAt), i18n.language, false)}
            emphasis={!item.readAt}
            accessibilityLabel={
              item.readAt ? undefined : `${tInbox(notificationKey(item.template, exists))}, ${tInbox('unread')}`
            }
          />
        )}
        empty={{ title: t('inbox.emptyTitle'), description: t('inbox.emptyDescription') }}
        header={
          <>
            <CheckinCard />
            <Heading>{t('inbox.heading')}</Heading>
          </>
        }
        testID="home-inbox"
      />
    </Screen>
  );
}
