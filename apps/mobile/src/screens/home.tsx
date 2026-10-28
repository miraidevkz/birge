// Home: a greeting, "how are you today" (routes on; stores nothing), the next
// weekly check-in as the one thing to do, the member's Circle, a gentle tip
// of the day and the latest inbox items with the way to all of them
// (ADR-0025: warm, personal, one action).
// "Hard" goes to Help: the crisis path is one tap from the first screen.
import { unwrap } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Button, Card, ChoiceGroup, Heading, List, Screen, Text } from '@birge/ui';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useCallback, useState } from 'react';
import { router } from 'expo-router';

import { api } from '@/lib/api';
import {
  openNotification,
  useInboxPreview,
  useRefreshInboxOnFocus,
  useUnreadCount,
  type Notification,
} from '@/lib/inbox';
import { nextCheckin } from '@/lib/next-checkin';

import { NotificationRow } from './notification-row';

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
    <Card tone="accent" testID="home-checkin">
      <Heading>{t('checkin.heading')}</Heading>
      <Text>{body}</Text>
      <Button
        title={t('checkin.action')}
        accessibilityHint={t('checkin.openLabel')}
        onPress={() => router.push('/checkin')}
        testID="home-checkin-start"
      />
    </Card>
  );
}

// 'none' is the unanswered state: no chip is selected.
type Mood = 'none' | 'good' | 'okay' | 'hard';

/** MoodCard asks how she is today and takes her to the right next step. */
function MoodCard() {
  const { t } = useTranslation('home');
  const [answer, setAnswer] = useState<Mood>('none');
  function pick(m: Mood) {
    setAnswer(m);
    if (m === 'okay') router.push('/checkin');
    if (m === 'hard') router.push('/sos');
  }
  return (
    <Card testID="home-mood">
      <ChoiceGroup<Mood>
        label={t('mood.question')}
        value={answer}
        onChange={pick}
        options={[
          { value: 'good', label: t('mood.good') },
          { value: 'okay', label: t('mood.okay') },
          { value: 'hard', label: t('mood.hard') },
        ]}
        testID="home-mood-choice"
      />
      {answer === 'good' ? (
        <Text tone="primary" accessibilityLiveRegion="polite">
          {t('mood.goodReply')}
        </Text>
      ) : null}
    </Card>
  );
}

/** CircleCard shows her tea match or search, or invites her into the Circle. */
function CircleCard() {
  const { t } = useTranslation('home');
  const matches = useQuery({ queryKey: ['matches'], queryFn: () => unwrap(api.GET('/me/matches')) });
  const tea = useQuery({ queryKey: ['tea-requests'], queryFn: () => unwrap(api.GET('/me/tea-requests')) });
  const match = matches.data?.find((m) => m.status === 'proposed' || m.status === 'accepted');
  const searching = tea.data?.some((r) => r.status === 'open');
  let body: string;
  if (match)
    body = t(match.status === 'accepted' ? 'circle.matchAccepted' : 'circle.match', { name: match.otherDisplayName });
  else if (searching) body = t('circle.searching');
  else body = t('circle.invite');
  return (
    <Card
      testID="home-circle"
      onPress={() => router.push('/circle')}
      accessibilityLabel={`${t('circle.heading')}. ${body}`}
    >
      <Heading>{t('circle.heading')}</Heading>
      <Text>{body}</Text>
      <Text variant="label" tone="primary">
        {t('circle.open')}
      </Text>
    </Card>
  );
}

/** AboutCard invites her to add district and children while the district is unset. */
function AboutCard({ show }: { show: boolean }) {
  const { t } = useTranslation('home');
  if (!show) return null;
  return (
    <Card
      testID="home-about"
      onPress={() => router.push('/about-me')}
      accessibilityLabel={`${t('about.heading')}. ${t('about.body')}`}
    >
      <Heading>{t('about.heading')}</Heading>
      <Text>{t('about.body')}</Text>
      <Text variant="label" tone="primary">
        {t('about.open')}
      </Text>
    </Card>
  );
}

/** TIP_COUNT is how many tips home.json has (tip.1 .. tip.N); one per day. */
const TIP_COUNT = 7;

/** TipCard is a short, gentle idea for the day, the same all day long. */
function TipCard() {
  const { t } = useTranslation('home');
  // The day is fixed when the card mounts: rendering stays pure.
  const [day] = useState(() => Math.floor(Date.now() / 86_400_000));
  return (
    <Card testID="home-tip">
      <Text variant="label" tone="muted">
        {t('tip.heading')}
      </Text>
      <Text>{t(`tip.${(day % TIP_COUNT) + 1}`)}</Text>
    </Card>
  );
}

/** HomeScreen is the first tab. */
export function HomeScreen() {
  const { t } = useTranslation('home');
  const { t: tInbox } = useTranslation('inbox');
  const client = useQueryClient();
  const inbox = useInboxPreview(INBOX_PREVIEW);
  const unread = useUnreadCount();
  useRefreshInboxOnFocus();
  const me = useQuery({ queryKey: ['me'], queryFn: () => unwrap(api.GET('/me')) });
  const name = me.data?.displayName?.trim();
  const open = useCallback((n: Notification) => void openNotification(client, n), [client]);
  const hasItems = (inbox.data?.items.length ?? 0) > 0;

  return (
    <Screen title={name ? t('greeting', { name }) : t('greetingNoName')} scroll={false}>
      <List<Notification>
        data={inbox.data?.items}
        loading={inbox.isLoading}
        error={inbox.error}
        onRetry={() => void inbox.refetch()}
        keyExtractor={(n) => n.id}
        renderItem={({ item }) => <NotificationRow item={item} onOpen={open} />}
        empty={{ title: t('inbox.emptyTitle'), description: t('inbox.emptyDescription') }}
        footer={
          hasItems ? (
            <Button
              title={unread > 0 ? tInbox('openUnread', { n: unread }) : tInbox('open')}
              variant="secondary"
              onPress={() => router.push('/inbox')}
              testID="home-inbox-open"
            />
          ) : undefined
        }
        header={
          <>
            <Text tone="muted">{t('howAreYou')}</Text>
            <MoodCard />
            <CheckinCard />
            <AboutCard show={me.data !== undefined && !me.data.districtId} />
            <CircleCard />
            <TipCard />
            <Heading>{t('inbox.heading')}</Heading>
          </>
        }
        testID="home-inbox"
      />
    </Screen>
  );
}
