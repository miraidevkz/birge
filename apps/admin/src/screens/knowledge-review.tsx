// Review queue: versions in_review. Publish or reject with a reason. The
// server enforces role and "not the author"; 403/409 become locale banners.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ChoiceGroup, List, Screen, Sheet, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';
import { knowledgeProblemKey } from '@/lib/knowledge-problem';

type StaffArticle = Schemas['StaffArticle'];
type RejectReason = NonNullable<Schemas['StaffArticle']['rejectReason']>;

const REASONS: RejectReason[] = ['inaccurate', 'outdated_law', 'unclear', 'unsafe_advice', 'missing_source', 'other'];

/** KnowledgeReviewScreen lists versions waiting for review and publish/reject actions. */
export function KnowledgeReviewScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [rejectingId, setRejectingId] = useState<string | null>(null);
  const [reason, setReason] = useState<RejectReason>('inaccurate');
  const [actionError, setActionError] = useState<string | null>(null);

  const queue = useQuery({
    queryKey: ['staff', 'knowledge', 'articles', 'in_review'],
    queryFn: () =>
      unwrap(
        api.GET('/staff/knowledge/articles', {
          params: { query: { status: 'in_review' } },
        }),
      ),
  });

  const publish = useMutation({
    mutationFn: (versionId: string) =>
      unwrap(
        api.POST('/staff/knowledge/versions/{versionId}/publish', {
          params: { path: { versionId } },
        }),
      ),
    onSuccess: async () => {
      setActionError(null);
      await client.invalidateQueries({ queryKey: ['staff', 'knowledge'] });
    },
    onError: (err) => setActionError(t(`knowledge.problem.${knowledgeProblemKey(err)}`)),
  });

  const reject = useMutation({
    mutationFn: (body: { versionId: string; reason: RejectReason }) =>
      unwrap(
        api.POST('/staff/knowledge/versions/{versionId}/reject', {
          params: { path: { versionId: body.versionId } },
          body: { reason: body.reason },
        }),
      ),
    onSuccess: async () => {
      setActionError(null);
      setRejectingId(null);
      await client.invalidateQueries({ queryKey: ['staff', 'knowledge'] });
    },
    onError: (err) => setActionError(t(`knowledge.problem.${knowledgeProblemKey(err)}`)),
  });

  return (
    <Screen title={t('knowledge.review.title')} scroll={false}>
      <List<StaffArticle>
        data={queue.data?.items}
        loading={queue.isLoading}
        error={queue.error}
        onRetry={() => void queue.refetch()}
        keyExtractor={(item) => item.versionId}
        empty={{
          title: t('knowledge.review.emptyTitle'),
          description: t('knowledge.review.emptyDescription'),
        }}
        testID="knowledge-review"
        header={
          actionError ? (
            <Banner tone="danger" testID="review-problem">
              {actionError}
            </Banner>
          ) : undefined
        }
        renderItem={({ item }) => (
          <Card testID={`review-${item.versionId}`}>
            <Text variant="label">{item.content.title}</Text>
            <Text tone="muted">{t(`knowledge.articles.kind.${item.kind}`)}</Text>
            <Text tone="muted">{t('knowledge.review.lang', { lang: item.lang })}</Text>
            <Text tone="muted">{t('knowledge.review.author', { id: item.authorId })}</Text>
            <Text>{item.content.body}</Text>
            <Button
              title={t('knowledge.review.publish')}
              onPress={() => publish.mutate(item.versionId)}
              loading={publish.isPending && publish.variables === item.versionId}
              testID={`publish-${item.versionId}`}
            />
            <Button
              title={t('knowledge.review.reject')}
              variant="secondary"
              onPress={() => {
                setRejectingId(item.versionId);
                setReason('inaccurate');
              }}
              testID={`reject-${item.versionId}`}
            />
          </Card>
        )}
      />
      <Sheet
        visible={rejectingId !== null}
        onClose={() => setRejectingId(null)}
        title={t('knowledge.review.rejectTitle')}
        testID="reject-sheet"
      >
        <ChoiceGroup
          label={t('knowledge.review.reason.label')}
          options={REASONS.map((r) => ({ value: r, label: t(`knowledge.review.reason.${r}`) }))}
          value={reason}
          onChange={setReason}
          testID="reject-reason"
        />
        <Button
          title={t('knowledge.review.confirmReject')}
          variant="destructive"
          onPress={() => {
            if (rejectingId) reject.mutate({ versionId: rejectingId, reason });
          }}
          loading={reject.isPending}
          testID="confirm-reject"
        />
      </Sheet>
    </Screen>
  );
}
