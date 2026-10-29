// Published versions whose review-due timer has fired, oldest first. Opening
// an item starts a new draft version in the editor. 403 (role/MFA) uses locale.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, List, Screen, Text } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';
import { router } from 'expo-router';

import { api } from '@/lib/api';
import { knowledgeProblemKey } from '@/lib/knowledge-problem';
import { formatDateTime } from '@/lib/time';

type StaffArticle = Schemas['StaffArticle'];

/** KnowledgeReviewDueScreen lists published versions that need a new review. */
export function KnowledgeReviewDueScreen() {
  const { t } = useTranslation('admin');

  const due = useQuery({
    queryKey: ['staff', 'knowledge', 'review-due'],
    queryFn: () => unwrap(api.GET('/staff/knowledge/review-due')),
  });

  if (due.isError && knowledgeProblemKey(due.error) === 'forbidden') {
    return (
      <Screen title={t('knowledge.reviewDue.title')}>
        <Banner tone="danger" testID="review-due-problem">
          {t('knowledge.problem.forbidden')}
        </Banner>
      </Screen>
    );
  }

  return (
    <Screen title={t('knowledge.reviewDue.title')} scroll={false}>
      <List<StaffArticle>
        data={due.data?.items}
        loading={due.isLoading}
        error={due.error}
        onRetry={() => void due.refetch()}
        keyExtractor={(item) => item.versionId}
        empty={{
          title: t('knowledge.reviewDue.emptyTitle'),
          description: t('knowledge.reviewDue.emptyDescription'),
        }}
        testID="knowledge-review-due"
        renderItem={({ item }) => (
          <Card testID={`due-${item.versionId}`}>
            <Text variant="label">{item.content.title}</Text>
            <Text tone="muted">{t(`knowledge.articles.kind.${item.kind}`)}</Text>
            <Text tone="muted">{t('knowledge.reviewDue.lang', { lang: item.lang })}</Text>
            {item.expiresAt ? (
              <Text tone="muted">{t('knowledge.reviewDue.expiresAt', { time: formatDateTime(item.expiresAt) })}</Text>
            ) : null}
            <Button
              title={t('knowledge.reviewDue.openEditor')}
              onPress={() =>
                router.push({
                  pathname: '/knowledge/editor',
                  params: { articleId: item.articleId },
                })
              }
              testID={`new-version-${item.versionId}`}
            />
          </Card>
        )}
      />
    </Screen>
  );
}
