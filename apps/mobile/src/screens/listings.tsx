// Simple member listing feed (sharing). Contact and create stay out of this screen.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { List, ListItem, Screen } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';

import { api } from '@/lib/api';

type Listing = Schemas['Listing'];

/** ListingsScreen shows active share listings. */
export function ListingsScreen() {
  const { t } = useTranslation('sharing');
  const listings = useQuery({
    queryKey: ['listings'],
    queryFn: () => unwrap(api.GET('/listings')),
  });

  return (
    <Screen title={t('title')} scroll={false}>
      <List<Listing>
        data={listings.data?.items}
        loading={listings.isLoading}
        error={listings.error}
        onRetry={() => void listings.refetch()}
        keyExtractor={(l) => l.id}
        renderItem={({ item }) => (
          <ListItem
            title={item.title}
            subtitle={`${t(`type.${item.type}`)} · ${item.category}`}
            testID={`listing-${item.id}`}
          />
        )}
        empty={{ title: t('emptyTitle'), description: t('emptyDescription') }}
        testID="listings"
      />
    </Screen>
  );
}
