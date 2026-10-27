// Draft editor for one language version: create, update, or start a new version
// of a published article (review-due). Body is plain Markdown TextField — no
// rich-text dependency. Server enforces author and role; 403/409 map to locale.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, ChoiceGroup, ErrorState, LoadingState, Screen, TextField } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useLocalSearchParams, router } from 'expo-router';
import { useState } from 'react';

import { api } from '@/lib/api';
import { knowledgeProblemKey } from '@/lib/knowledge-problem';

type KnowledgeKind = Schemas['KnowledgeKind'];
type Lang = Schemas['Lang'];
type StaffArticle = Schemas['StaffArticle'];

const KINDS: KnowledgeKind[] = ['rights', 'selfhelp'];
const LANGS: Lang[] = ['kk', 'ru', 'en'];

type EditorSeed = {
  versionId: string | null;
  articleId?: string;
  kind: KnowledgeKind;
  topicId: string;
  lang: Lang;
  title: string;
  body: string;
  sourceUrl: string;
  locked: boolean;
};

/** KnowledgeEditorScreen creates or updates a draft and can submit it for review. */
export function KnowledgeEditorScreen() {
  const { t } = useTranslation('admin');
  const params = useLocalSearchParams<{
    versionId?: string | string[];
    articleId?: string | string[];
  }>();
  const versionIdParam = first(params.versionId);
  const articleIdParam = first(params.articleId);

  const topics = useQuery({
    queryKey: ['staff', 'knowledge', 'topics'],
    queryFn: () => unwrap(api.GET('/staff/knowledge/topics')),
  });

  const drafts = useQuery({
    queryKey: ['staff', 'knowledge', 'articles', 'draft'],
    queryFn: () => unwrap(api.GET('/staff/knowledge/articles', { params: { query: { status: 'draft' } } })),
    enabled: Boolean(versionIdParam),
  });

  const reviewDue = useQuery({
    queryKey: ['staff', 'knowledge', 'review-due'],
    queryFn: () => unwrap(api.GET('/staff/knowledge/review-due')),
    enabled: Boolean(articleIdParam) && !versionIdParam,
  });

  const screenTitle = versionIdParam
    ? t('knowledge.editor.titleEdit')
    : articleIdParam
      ? t('knowledge.editor.titleNewVersion')
      : t('knowledge.editor.titleNew');

  if (
    topics.isLoading ||
    (versionIdParam && drafts.isLoading) ||
    (articleIdParam && !versionIdParam && reviewDue.isLoading)
  ) {
    return (
      <Screen title={screenTitle}>
        <LoadingState />
      </Screen>
    );
  }

  if (topics.isError) {
    return (
      <Screen title={screenTitle}>
        <ErrorState onRetry={() => void topics.refetch()} />
      </Screen>
    );
  }

  let seed: EditorSeed | null = {
    versionId: null,
    kind: 'selfhelp',
    topicId: '',
    lang: 'kk',
    title: '',
    body: '',
    sourceUrl: '',
    locked: false,
  };

  if (versionIdParam) {
    if (drafts.isError) {
      return (
        <Screen title={screenTitle}>
          <ErrorState onRetry={() => void drafts.refetch()} />
        </Screen>
      );
    }
    const found = drafts.data?.items.find((item) => item.versionId === versionIdParam);
    if (!found) {
      return (
        <Screen title={screenTitle}>
          <Banner tone="danger">{t('knowledge.problem.failed')}</Banner>
        </Screen>
      );
    }
    seed = seedFromArticle(found, true);
  } else if (articleIdParam) {
    if (reviewDue.isError) {
      const key = knowledgeProblemKey(reviewDue.error);
      if (key === 'forbidden') {
        return (
          <Screen title={screenTitle}>
            <Banner tone="danger">{t('knowledge.problem.forbidden')}</Banner>
          </Screen>
        );
      }
      return (
        <Screen title={screenTitle}>
          <ErrorState onRetry={() => void reviewDue.refetch()} />
        </Screen>
      );
    }
    const found = reviewDue.data?.items.find((item) => item.articleId === articleIdParam);
    if (!found) {
      return (
        <Screen title={screenTitle}>
          <Banner tone="danger">{t('knowledge.problem.failed')}</Banner>
        </Screen>
      );
    }
    seed = {
      ...seedFromArticle(found, true),
      versionId: null,
      articleId: found.articleId,
    };
  } else {
    const firstTopic = (topics.data ?? []).find((topic) => topic.kind === 'selfhelp');
    seed = {
      ...seed,
      topicId: firstTopic?.id ?? '',
    };
  }

  return (
    <KnowledgeEditorForm
      key={`${versionIdParam ?? ''}:${articleIdParam ?? ''}:${seed.versionId ?? 'new'}`}
      title={screenTitle}
      seed={seed}
      topics={topics.data ?? []}
    />
  );
}

function seedFromArticle(item: StaffArticle, locked: boolean): EditorSeed {
  return {
    versionId: item.versionId,
    articleId: item.articleId,
    kind: item.kind,
    topicId: item.topicId,
    lang: item.lang,
    title: item.content.title,
    body: item.content.body,
    sourceUrl: item.content.sourceUrl ?? '',
    locked,
  };
}

function KnowledgeEditorForm({
  title: screenTitle,
  seed,
  topics,
}: {
  title: string;
  seed: EditorSeed;
  topics: Schemas['KnowledgeTopic'][];
}) {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [kind, setKind] = useState<KnowledgeKind>(seed.kind);
  const [topicId, setTopicId] = useState(seed.topicId);
  const [lang, setLang] = useState<Lang>(seed.lang);
  const [title, setTitle] = useState(seed.title);
  const [body, setBody] = useState(seed.body);
  const [sourceUrl, setSourceUrl] = useState(seed.sourceUrl);
  const [versionId, setVersionId] = useState<string | null>(seed.versionId);
  const [articleId, setArticleId] = useState<string | undefined>(seed.articleId);
  const [locked] = useState(seed.locked);
  const [invalid, setInvalid] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);

  const topicOptions = topics
    .filter((topic) => topic.kind === kind)
    .map((topic) => ({ value: topic.id, label: topic.title }));
  const effectiveTopicId = topicOptions.some((opt) => opt.value === topicId) ? topicId : (topicOptions[0]?.value ?? '');

  const save = useMutation({
    mutationFn: async () => {
      const content = {
        title: title.trim(),
        body: body.trim(),
        sourceUrl: sourceUrl.trim() === '' ? undefined : sourceUrl.trim(),
      };
      if (versionId) {
        return unwrap(
          api.PUT('/staff/knowledge/versions/{versionId}', {
            params: { path: { versionId } },
            body: content,
          }),
        );
      }
      return unwrap(
        api.POST('/staff/knowledge/articles', {
          body: {
            articleId,
            topicId: effectiveTopicId,
            lang,
            content,
          },
        }),
      );
    },
    onSuccess: async (article) => {
      setActionError(null);
      setInfo(t('knowledge.editor.saved'));
      setVersionId(article.versionId);
      setArticleId(article.articleId);
      // Do not invalidate topics: a refetch failure would remount the form.
      await client.invalidateQueries({ queryKey: ['staff', 'knowledge', 'articles'] });
      await client.invalidateQueries({ queryKey: ['staff', 'knowledge', 'review-due'] });
    },
    onError: (err) => {
      setInfo(null);
      setActionError(t(`knowledge.problem.${knowledgeProblemKey(err)}`));
    },
  });

  const submit = useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.POST('/staff/knowledge/versions/{versionId}/submit', {
          params: { path: { versionId: id } },
        }),
      ),
    onSuccess: async () => {
      setActionError(null);
      setInfo(t('knowledge.editor.submitted'));
      await client.invalidateQueries({ queryKey: ['staff', 'knowledge', 'articles'] });
      await client.invalidateQueries({ queryKey: ['staff', 'knowledge', 'review-due'] });
      router.back();
    },
    onError: (err) => {
      setInfo(null);
      setActionError(t(`knowledge.problem.${knowledgeProblemKey(err)}`));
    },
  });

  function onSave() {
    if (title.trim() === '' || body.trim() === '' || effectiveTopicId === '') {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    save.mutate();
  }

  function onKindChange(next: KnowledgeKind) {
    setKind(next);
    const firstTopic = topics.find((topic) => topic.kind === next);
    setTopicId(firstTopic?.id ?? '');
  }

  return (
    <Screen
      title={screenTitle}
      footer={
        <>
          <Button title={t('knowledge.editor.save')} onPress={onSave} loading={save.isPending} testID="editor-save" />
          {versionId ? (
            <Button
              title={t('knowledge.editor.submit')}
              variant="secondary"
              onPress={() => submit.mutate(versionId)}
              loading={submit.isPending}
              testID="editor-submit"
            />
          ) : null}
        </>
      }
    >
      {!locked ? (
        <ChoiceGroup
          label={t('knowledge.editor.kind.label')}
          options={KINDS.map((k) => ({ value: k, label: t(`knowledge.editor.kind.${k}`) }))}
          value={kind}
          onChange={onKindChange}
          testID="editor-kind"
        />
      ) : null}
      {topicOptions.length > 0 ? (
        <ChoiceGroup
          label={t('knowledge.editor.topic.label')}
          options={topicOptions}
          value={effectiveTopicId}
          onChange={setTopicId}
          disabled={locked}
          testID="editor-topic"
        />
      ) : null}
      <ChoiceGroup
        label={t('knowledge.editor.lang.label')}
        options={LANGS.map((l) => ({ value: l, label: t(`knowledge.editor.lang.${l}`) }))}
        value={lang}
        onChange={setLang}
        disabled={locked}
        testID="editor-lang"
      />
      <TextField
        label={t('knowledge.editor.articleTitle.label')}
        hint={t('knowledge.editor.articleTitle.hint')}
        value={title}
        onChangeText={setTitle}
        maxLength={200}
        testID="editor-title"
      />
      <TextField
        label={t('knowledge.editor.body.label')}
        hint={t('knowledge.editor.body.hint')}
        value={body}
        onChangeText={setBody}
        multiline
        numberOfLines={10}
        maxLength={20000}
        testID="editor-body"
      />
      <TextField
        label={t('knowledge.editor.sourceUrl.label')}
        hint={t('knowledge.editor.sourceUrl.hint')}
        value={sourceUrl}
        onChangeText={setSourceUrl}
        autoCapitalize="none"
        maxLength={2048}
        testID="editor-source"
      />
      {invalid ? <Banner tone="danger">{t('knowledge.editor.invalid')}</Banner> : null}
      {actionError ? (
        <Banner tone="danger" testID="editor-problem">
          {actionError}
        </Banner>
      ) : null}
      {info ? <Banner tone="success">{info}</Banner> : null}
    </Screen>
  );
}

function first(value: string | string[] | undefined): string | undefined {
  if (Array.isArray(value)) return value[0];
  return value;
}
