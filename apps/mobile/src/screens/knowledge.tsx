// Knowledge: topic list for a kind, then the published articles of a topic.
import { unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation, type Language } from '@birge/i18n';
import { Button, ChoiceGroup, Heading, List, ListItem, Screen } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useState } from 'react';

import { api } from '@/lib/api';

type KnowledgeKind = Schemas['KnowledgeKind'];
type KnowledgeTopic = Schemas['KnowledgeTopic'];
type ArticleSummary = Schemas['ArticleSummary'];

function lang(): Language {
  return (i18n.language === 'ru' || i18n.language === 'en' ? i18n.language : 'kk') as Language;
}

/** KnowledgeScreen lists topics, then articles for the selected topic. */
export function KnowledgeScreen() {
  const { t } = useTranslation('knowledge');
  const [kind, setKind] = useState<KnowledgeKind>('rights');
  const [topic, setTopic] = useState<KnowledgeTopic | null>(null);
  const currentLang = lang();

  const topics = useQuery({
    queryKey: ['knowledge', 'topics', kind, currentLang],
    queryFn: () =>
      unwrap(
        api.GET('/knowledge/topics', {
          params: { query: { lang: currentLang, kind } },
        }),
      ),
  });

  const articles = useQuery({
    queryKey: ['knowledge', 'articles', topic?.id, currentLang],
    queryFn: () =>
      unwrap(
        api.GET('/knowledge/topics/{topicId}/articles', {
          params: { path: { topicId: topic!.id }, query: { lang: currentLang } },
        }),
      ),
    enabled: topic !== null,
  });

  if (topic) {
    return (
      <Screen title={topic.title} scroll={false}>
        <List<ArticleSummary>
          data={articles.data}
          loading={articles.isLoading}
          error={articles.error}
          onRetry={() => void articles.refetch()}
          keyExtractor={(a) => a.id}
          renderItem={({ item }) => (
            <ListItem
              title={item.title}
              subtitle={item.status === 'being_rechecked' ? t('articles.rechecking') : undefined}
              onPress={() => router.push(`/knowledge-article/${item.id}`)}
              testID={`knowledge-article-${item.id}`}
            />
          )}
          empty={{ title: t('articles.emptyTitle'), description: t('articles.emptyDescription') }}
          header={
            <>
              <Button
                title={t('topics.back')}
                variant="secondary"
                onPress={() => setTopic(null)}
                testID="knowledge-back-topics"
              />
              <Heading>{t('articles.heading')}</Heading>
            </>
          }
          testID="knowledge-articles"
        />
      </Screen>
    );
  }

  return (
    <Screen title={t('title')} scroll={false}>
      <List<KnowledgeTopic>
        data={topics.data}
        loading={topics.isLoading}
        error={topics.error}
        onRetry={() => void topics.refetch()}
        keyExtractor={(item) => item.id}
        renderItem={({ item }) => (
          <ListItem title={item.title} onPress={() => setTopic(item)} testID={`knowledge-topic-${item.id}`} />
        )}
        empty={{ title: t('topics.emptyTitle'), description: t('topics.emptyDescription') }}
        header={
          <>
            <Button
              title={t('article.back')}
              variant="secondary"
              onPress={() => router.back()}
              testID="knowledge-back"
            />
            <ChoiceGroup
              label={t('kinds.heading')}
              value={kind}
              onChange={(next) => {
                setKind(next);
                setTopic(null);
              }}
              options={[
                { value: 'rights', label: t('kinds.rights') },
                { value: 'selfhelp', label: t('kinds.selfhelp') },
              ]}
              testID="knowledge-kind"
            />
            <Heading>{t('topics.heading')}</Heading>
          </>
        }
        testID="knowledge-topics"
      />
    </Screen>
  );
}
