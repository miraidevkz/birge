// Knowledge article view: the published body for one article id.
import { unwrap } from '@birge/api';
import { i18n, useTranslation, type Language } from '@birge/i18n';
import { Banner, Button, ErrorState, LoadingState, Screen, Text } from '@birge/ui';
import { useQuery } from '@tanstack/react-query';
import { Linking } from 'react-native';

import { api } from '@/lib/api';

function lang(): Language {
  return (i18n.language === 'ru' || i18n.language === 'en' ? i18n.language : 'kk') as Language;
}

function formatDate(iso: string, language: string): string {
  return new Intl.DateTimeFormat(language, { day: 'numeric', month: 'long', year: 'numeric' }).format(new Date(iso));
}

/** KnowledgeArticleScreen shows one published article. */
export function KnowledgeArticleScreen({ articleId }: { articleId: string }) {
  const { t } = useTranslation('knowledge');
  const currentLang = lang();
  const article = useQuery({
    queryKey: ['knowledge', 'article', articleId, currentLang],
    queryFn: () =>
      unwrap(
        api.GET('/knowledge/articles/{articleId}', {
          params: { path: { articleId }, query: { lang: currentLang } },
        }),
      ),
  });

  if (article.isLoading && !article.data) {
    return (
      <Screen title={t('article.title')}>
        <LoadingState />
      </Screen>
    );
  }

  if (article.isError && !article.data) {
    return (
      <Screen title={t('article.title')}>
        <ErrorState title={t('article.notFound')} onRetry={() => void article.refetch()} />
      </Screen>
    );
  }

  const data = article.data!;
  return (
    <Screen title={data.title}>
      {data.status === 'being_rechecked' ? <Banner tone="warning">{t('article.rechecking')}</Banner> : null}
      <Text tone="muted">{t('article.reviewed', { date: formatDate(data.reviewedAt, i18n.language) })}</Text>
      <Text testID="article-body">{data.body}</Text>
      {data.sourceUrl ? (
        <Button
          title={t('article.source')}
          variant="secondary"
          onPress={() => void Linking.openURL(data.sourceUrl!)}
          testID="article-source"
        />
      ) : null}
    </Screen>
  );
}
