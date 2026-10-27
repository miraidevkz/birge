// Past weekly check-in scores (default last 8 weeks). Scores only; never answers.
import { unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { List, ListItem, Screen } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';

import { api } from '@/lib/api';

type CheckinPoint = Schemas['CheckinPoint'];

function formatDay(iso: string, lang: string): string {
  return new Intl.DateTimeFormat(lang, {
    weekday: 'short',
    day: 'numeric',
    month: 'long',
    year: 'numeric',
  }).format(new Date(iso));
}

/** CheckinHistoryScreen lists completed check-in score points, oldest first. */
export function CheckinHistoryScreen() {
  const { t } = useTranslation('wellbeing');
  const history = useQuery({
    queryKey: ['checkins', 'history'],
    queryFn: () => unwrap(api.GET('/me/checkins/history', { params: { query: { weeks: 8 } } })),
  });

  return (
    <Screen title={t('history.title')} scroll={false}>
      <List<CheckinPoint>
        data={history.data}
        loading={history.isLoading}
        error={history.error}
        onRetry={() => void history.refetch()}
        keyExtractor={(p) => p.completedAt}
        renderItem={({ item }) => (
          <ListItem
            title={formatDay(item.completedAt, i18n.language)}
            subtitle={t('history.point', { phq4: item.phq4, pss4: item.pss4 })}
          />
        )}
        empty={{ title: t('history.emptyTitle'), description: t('history.emptyDescription') }}
        testID="checkin-history"
      />
    </Screen>
  );
}
