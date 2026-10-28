// Privacy: consents list/change and account deletion request with a confirm Sheet.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Heading, List, ListItem, Screen, Sheet, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useState } from 'react';

import { api } from '@/lib/api';
import { useSession } from '@/lib/session';

type Consent = Schemas['Consent'];
type ConsentType = Schemas['ConsentType'];

/** PrivacyScreen lists consents and lets the member request account deletion. */
export function PrivacyScreen() {
  const { t } = useTranslation('privacy');
  const session = useSession();
  const client = useQueryClient();
  const [sheetOpen, setSheetOpen] = useState(false);
  const [deletionFailed, setDeletionFailed] = useState(false);
  const [busyType, setBusyType] = useState<ConsentType | null>(null);

  const consents = useQuery({
    queryKey: ['consents'],
    queryFn: () => unwrap(api.GET('/me/consents')),
  });

  const setConsent = useMutation({
    mutationFn: (body: Schemas['ConsentChange']) => unwrap(api.POST('/me/consents', { body })),
    onMutate: (body) => setBusyType(body.type),
    onSettled: () => setBusyType(null),
    onSuccess: (updated) => {
      client.setQueryData<Consent[]>(['consents'], (prev) =>
        prev ? prev.map((c) => (c.type === updated.type ? updated : c)) : [updated],
      );
    },
  });

  const requestDeletion = useMutation({
    mutationFn: () => unwrap(api.POST('/me/deletion-request', { body: { confirm: 'DELETE' } })),
    onSuccess: async () => {
      setSheetOpen(false);
      await session.signOut();
      router.replace('/sign-in');
    },
    onError: () => {
      setDeletionFailed(true);
    },
  });

  return (
    <Screen
      title={t('title')}
      scroll={false}
      footer={
        <>
          <Heading>{t('deletion.heading')}</Heading>
          <Text tone="muted">{t('deletion.description')}</Text>
          <Button
            title={t('deletion.action')}
            variant="destructive"
            onPress={() => {
              setDeletionFailed(false);
              setSheetOpen(true);
            }}
            testID="privacy-delete-open"
          />
        </>
      }
    >
      <List<Consent>
        data={consents.data}
        loading={consents.isLoading}
        error={consents.error}
        onRetry={() => void consents.refetch()}
        keyExtractor={(c) => c.type}
        renderItem={({ item }) => (
          <ListItem
            title={t(`consents.types.${item.type}`)}
            subtitle={item.granted ? t('consents.granted') : t('consents.revoked')}
            trailing={
              <Button
                title={item.granted ? t('consents.revoke') : t('consents.grant')}
                variant="secondary"
                size="sm"
                loading={busyType === item.type}
                disabled={busyType !== null && busyType !== item.type}
                onPress={() =>
                  setConsent.mutate({
                    type: item.type,
                    version: item.currentVersion,
                    granted: !item.granted,
                  })
                }
                testID={`privacy-consent-${item.type}`}
              />
            }
          />
        )}
        empty={{ title: t('consents.emptyTitle'), description: t('consents.emptyDescription') }}
        header={
          <>
            <Heading>{t('consents.heading')}</Heading>
          </>
        }
        testID="privacy-consents"
      />

      <Sheet
        visible={sheetOpen}
        onClose={() => setSheetOpen(false)}
        title={t('deletion.sheetTitle')}
        testID="privacy-delete-sheet"
      >
        <Text>{t('deletion.sheetBody')}</Text>
        {deletionFailed ? <Banner tone="danger">{t('deletion.failed')}</Banner> : null}
        <Button
          title={t('deletion.confirm')}
          variant="destructive"
          loading={requestDeletion.isPending}
          onPress={() => requestDeletion.mutate()}
          testID="privacy-delete-confirm"
        />
      </Sheet>
    </Screen>
  );
}
