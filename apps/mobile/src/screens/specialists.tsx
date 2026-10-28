// Simple specialist directory list. Contacts are the specialist's public work
// contacts from the API; this screen never suggests leaving a child with anyone.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { List, ListItem, Screen } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';

import { api } from '@/lib/api';
import { professionLabel } from '@/lib/reference';

type Specialist = Schemas['Specialist'];

/** SpecialistsScreen shows the reviewed specialist directory. */
export function SpecialistsScreen() {
  const { t } = useTranslation('directory');
  const specialists = useQuery({
    queryKey: ['specialists'],
    queryFn: () => unwrap(api.GET('/specialists')),
  });

  return (
    <Screen title={t('title')} scroll={false}>
      <List<Specialist>
        data={specialists.data?.items}
        loading={specialists.isLoading}
        error={specialists.error}
        onRetry={() => void specialists.refetch()}
        keyExtractor={(s) => s.id}
        renderItem={({ item }) => {
          const rating =
            item.averageRating !== undefined
              ? t('rating', { rating: String(item.averageRating), count: String(item.reviewCount) })
              : t('noRating');
          return (
            <ListItem
              title={item.name}
              subtitle={`${professionLabel(item.profession)} · ${rating}`}
              testID={`specialist-${item.id}`}
            />
          );
        }}
        empty={{ title: t('emptyTitle'), description: t('emptyDescription') }}
        testID="specialists"
      />
    </Screen>
  );
}
