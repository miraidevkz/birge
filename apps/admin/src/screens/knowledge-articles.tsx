// Staff knowledge articles list: filter by kind (client) and status (API),
// open the draft editor, review queue and review-due list. Topic create lives
// here so authors can add a topic before drafting.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ChoiceGroup, List, Screen, Sheet, Text, TextField } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useMemo, useState } from 'react';

import { api } from '@/lib/api';
import { knowledgeProblemKey } from '@/lib/knowledge-problem';
import { formatDateTime } from '@/lib/time';

type StaffArticle = Schemas['StaffArticle'];
type KnowledgeKind = Schemas['KnowledgeKind'];
type ArticleVersionStatus = Schemas['ArticleVersionStatus'];

type KindFilter = 'all' | KnowledgeKind;
type StatusFilter = 'all' | ArticleVersionStatus;

const KINDS: KindFilter[] = ['all', 'rights', 'selfhelp'];
const STATUSES: StatusFilter[] = ['all', 'draft', 'in_review', 'published', 'retired'];
const TOPIC_KINDS: KnowledgeKind[] = ['rights', 'selfhelp'];
const TOPIC_CODE = /^[a-z_]{2,40}$/;

/** KnowledgeArticlesScreen lists staff article versions with kind and status filters. */
export function KnowledgeArticlesScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [kind, setKind] = useState<KindFilter>('all');
  const [status, setStatus] = useState<StatusFilter>('all');
  const [topicOpen, setTopicOpen] = useState(false);
  const [topicKind, setTopicKind] = useState<KnowledgeKind>('selfhelp');
  const [topicCode, setTopicCode] = useState('');
  const [titleKk, setTitleKk] = useState('');
  const [titleRu, setTitleRu] = useState('');
  const [titleEn, setTitleEn] = useState('');
  const [topicInvalid, setTopicInvalid] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const list = useQuery({
    queryKey: ['staff', 'knowledge', 'articles', status],
    queryFn: () =>
      unwrap(
        api.GET('/staff/knowledge/articles', {
          params: {
            query: status === 'all' ? {} : { status },
          },
        }),
      ),
  });

  const filtered = useMemo(() => {
    const items = list.data?.items;
    if (items === undefined) return undefined;
    if (kind === 'all') return items;
    return items.filter((item) => item.kind === kind);
  }, [list.data?.items, kind]);

  const createTopic = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST('/staff/knowledge/topics', {
          body: {
            kind: topicKind,
            code: topicCode.trim(),
            titles: {
              kk: titleKk.trim() || undefined,
              ru: titleRu.trim() || undefined,
              en: titleEn.trim() || undefined,
            },
          },
        }),
      ),
    onSuccess: async () => {
      setActionError(null);
      setTopicOpen(false);
      setTopicCode('');
      setTitleKk('');
      setTitleRu('');
      setTitleEn('');
      await client.invalidateQueries({ queryKey: ['staff', 'knowledge', 'topics'] });
    },
    onError: (err) => setActionError(t(`knowledge.problem.${knowledgeProblemKey(err)}`)),
  });

  function submitTopic() {
    if (
      !TOPIC_CODE.test(topicCode.trim()) ||
      (titleKk.trim() === '' && titleRu.trim() === '' && titleEn.trim() === '')
    ) {
      setTopicInvalid(true);
      return;
    }
    setTopicInvalid(false);
    createTopic.mutate();
  }

  return (
    <Screen title={t('knowledge.articles.title')} scroll={false}>
      <List<StaffArticle>
        data={filtered}
        loading={list.isLoading}
        error={list.error}
        onRetry={() => void list.refetch()}
        keyExtractor={(item) => item.versionId}
        empty={{
          title: t('knowledge.articles.emptyTitle'),
          description: t('knowledge.articles.emptyDescription'),
        }}
        testID="knowledge-articles"
        header={
          <>
            <ChoiceGroup
              label={t('knowledge.articles.kind.label')}
              options={KINDS.map((k) => ({ value: k, label: t(`knowledge.articles.kind.${k}`) }))}
              value={kind}
              onChange={setKind}
              testID="knowledge-kind"
            />
            <ChoiceGroup
              label={t('knowledge.articles.status.label')}
              options={STATUSES.map((s) => ({ value: s, label: t(`knowledge.articles.status.${s}`) }))}
              value={status}
              onChange={setStatus}
              testID="knowledge-status"
            />
            {actionError ? <Banner tone="danger">{actionError}</Banner> : null}
            <Button
              title={t('knowledge.articles.newDraft')}
              onPress={() => router.push('/knowledge/editor')}
              testID="knowledge-new-draft"
            />
            <Button
              title={t('knowledge.articles.createTopic')}
              variant="secondary"
              onPress={() => setTopicOpen(true)}
              testID="knowledge-create-topic"
            />
            <Button
              title={t('knowledge.articles.openReview')}
              variant="secondary"
              onPress={() => router.push('/knowledge/review')}
              testID="knowledge-open-review"
            />
            <Button
              title={t('knowledge.articles.openReviewDue')}
              variant="secondary"
              onPress={() => router.push('/knowledge/review-due')}
              testID="knowledge-open-review-due"
            />
          </>
        }
        renderItem={({ item }) => (
          <Card testID={`article-${item.versionId}`}>
            <Text variant="label">{item.content.title}</Text>
            <Text tone="muted">{t(`knowledge.articles.kind.${item.kind}`)}</Text>
            <Text tone="muted">{t(`knowledge.articles.status.${item.status}`)}</Text>
            <Text tone="muted">{t('knowledge.articles.lang', { lang: item.lang })}</Text>
            <Text tone="muted">{t('knowledge.articles.updatedAt', { time: formatDateTime(item.updatedAt) })}</Text>
            {item.status === 'draft' ? (
              <Button
                title={t('knowledge.articles.edit')}
                variant="secondary"
                onPress={() =>
                  router.push({
                    pathname: '/knowledge/editor',
                    params: { versionId: item.versionId },
                  })
                }
                testID={`edit-${item.versionId}`}
              />
            ) : null}
          </Card>
        )}
      />
      <Sheet
        visible={topicOpen}
        onClose={() => setTopicOpen(false)}
        title={t('knowledge.topic.title')}
        testID="topic-sheet"
      >
        <ChoiceGroup
          label={t('knowledge.topic.kind.label')}
          options={TOPIC_KINDS.map((k) => ({ value: k, label: t(`knowledge.topic.kind.${k}`) }))}
          value={topicKind}
          onChange={setTopicKind}
          testID="topic-kind"
        />
        <TextField
          label={t('knowledge.topic.code.label')}
          hint={t('knowledge.topic.code.hint')}
          value={topicCode}
          onChangeText={setTopicCode}
          autoCapitalize="none"
          testID="topic-code"
        />
        <TextField
          label={t('knowledge.topic.titleKk.label')}
          value={titleKk}
          onChangeText={setTitleKk}
          testID="topic-title-kk"
        />
        <TextField
          label={t('knowledge.topic.titleRu.label')}
          value={titleRu}
          onChangeText={setTitleRu}
          testID="topic-title-ru"
        />
        <TextField
          label={t('knowledge.topic.titleEn.label')}
          value={titleEn}
          onChangeText={setTitleEn}
          testID="topic-title-en"
        />
        {topicInvalid ? <Banner tone="danger">{t('knowledge.topic.invalid')}</Banner> : null}
        <Button
          title={t('knowledge.topic.save')}
          onPress={submitTopic}
          loading={createTopic.isPending}
          testID="topic-save"
        />
      </Sheet>
    </Screen>
  );
}
