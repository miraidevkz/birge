// Moderation queue: oldest first. Decisions follow the contract enum
// (approve / reject with reason). Summary text is content under review, not
// contact data; still never logged.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ChoiceGroup, List, Screen, Sheet, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';

type ModerationItem = Schemas['ModerationItem'];
type Reason = NonNullable<Schemas['ModerationDecision']['reason']>;

const REASONS: Reason[] = [
  'rules_medicine',
  'rules_money',
  'person_in_photo',
  'unsafe',
  'spam',
  'inaccurate',
  'duplicate',
  'other',
];

/** ModerationScreen lets a moderator approve or reject queue items. */
export function ModerationScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [rejectingId, setRejectingId] = useState<string | null>(null);
  const [reason, setReason] = useState<Reason>('spam');
  const [actionError, setActionError] = useState(false);

  const queue = useQuery({
    queryKey: ['staff', 'moderation'],
    queryFn: () => unwrap(api.GET('/staff/moderation/queue')),
  });

  const decide = useMutation({
    mutationFn: (body: { itemId: string; approve: boolean; reason?: Reason }) =>
      unwrap(
        api.POST('/staff/moderation/items/{itemId}/decision', {
          params: { path: { itemId: body.itemId } },
          body: body.approve ? { approve: true } : { approve: false, reason: body.reason },
        }),
      ),
    onSuccess: async () => {
      setActionError(false);
      setRejectingId(null);
      await client.invalidateQueries({ queryKey: ['staff', 'moderation'] });
    },
    onError: () => setActionError(true),
  });

  return (
    <Screen title={t('moderation.title')} scroll={false}>
      <List<ModerationItem>
        data={queue.data?.items}
        loading={queue.isLoading}
        error={queue.error}
        onRetry={() => void queue.refetch()}
        keyExtractor={(item) => item.id}
        empty={{ title: t('moderation.emptyTitle'), description: t('moderation.emptyDescription') }}
        testID="moderation"
        header={actionError ? <Banner tone="danger">{t('signIn.failed')}</Banner> : undefined}
        renderItem={({ item }) => (
          <Card testID={`moderation-${item.id}`}>
            <Text variant="label">{t(`moderation.kind.${item.kind}`)}</Text>
            <Text>{item.summary}</Text>
            <Text tone="muted">{item.createdAt}</Text>
            <Button
              title={t('moderation.approve')}
              onPress={() => decide.mutate({ itemId: item.id, approve: true })}
              loading={decide.isPending && decide.variables?.itemId === item.id && decide.variables.approve}
              testID={`approve-${item.id}`}
            />
            <Button
              title={t('moderation.reject')}
              variant="secondary"
              onPress={() => {
                setRejectingId(item.id);
                setReason('spam');
              }}
              testID={`reject-${item.id}`}
            />
          </Card>
        )}
      />
      <Sheet
        visible={rejectingId !== null}
        onClose={() => setRejectingId(null)}
        title={t('moderation.rejectTitle')}
        testID="reject-sheet"
      >
        <ChoiceGroup
          label={t('moderation.reason.label')}
          options={REASONS.map((r) => ({ value: r, label: t(`moderation.reason.${r}`) }))}
          value={reason}
          onChange={setReason}
          testID="reject-reason"
        />
        <Button
          title={t('moderation.confirmReject')}
          variant="destructive"
          onPress={() => {
            if (rejectingId) decide.mutate({ itemId: rejectingId, approve: false, reason });
          }}
          loading={decide.isPending}
          testID="confirm-reject"
        />
      </Sheet>
    </Screen>
  );
}
