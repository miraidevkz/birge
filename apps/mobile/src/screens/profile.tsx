// Profile: view and edit the member, switch language, open related screens, sign out.
import { unwrap, type Schemas } from '@birge/api';
import { i18n, languages, useTranslation, type Language } from '@birge/i18n';
import {
  Banner,
  Button,
  Card,
  ChoiceGroup,
  ErrorState,
  Heading,
  ListItem,
  LoadingState,
  Screen,
  Text,
  TextField,
} from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useEffect, useState } from 'react';

import { api } from '@/lib/api';
import { useSession } from '@/lib/session';

type Member = Schemas['Member'];

/** ProfileScreen shows and edits the signed-in member. */
export function ProfileScreen() {
  const { t } = useTranslation('profile');
  const session = useSession();
  const client = useQueryClient();
  const me = useQuery({
    queryKey: ['me'],
    queryFn: () => unwrap(api.GET('/me')),
  });
  // Local draft overlays the server value so typing does not fight refetch; null means "show server".
  const [draftName, setDraftName] = useState<string | null>(null);
  const name = draftName ?? me.data?.displayName ?? '';
  const [status, setStatus] = useState<'idle' | 'saved' | 'failed'>('idle');

  useEffect(() => {
    if (!me.data) return;
    // Keep the UI language in sync with the profile when it loads (external i18n system).
    if (me.data.lang !== i18n.language) void i18n.changeLanguage(me.data.lang);
  }, [me.data]);

  const saveName = useMutation({
    mutationFn: (displayName: string) => unwrap(api.PATCH('/me', { body: { displayName } })),
    onSuccess: (member) => {
      client.setQueryData(['me'], member);
      setDraftName(null);
      setStatus('saved');
    },
    onError: () => setStatus('failed'),
  });

  const saveLang = useMutation({
    mutationFn: (lang: Language) => unwrap(api.PATCH('/me', { body: { lang } })),
    onSuccess: (member) => {
      client.setQueryData(['me'], member);
      void i18n.changeLanguage(member.lang);
      setStatus('saved');
    },
    onError: () => setStatus('failed'),
  });

  async function signOut() {
    await session.signOut();
    router.replace('/sign-in');
  }

  if (me.isLoading && !me.data) {
    return (
      <Screen title={t('title')}>
        <LoadingState />
      </Screen>
    );
  }

  if (me.isError && !me.data) {
    return (
      <Screen title={t('title')}>
        <ErrorState onRetry={() => void me.refetch()} />
      </Screen>
    );
  }

  const member = me.data as Member;
  const lang = (saveLang.isPending ? i18n.language : member.lang) as Language;

  return (
    <Screen
      title={t('title')}
      footer={
        <Button title={t('signOut')} variant="secondary" onPress={() => void signOut()} testID="profile-sign-out" />
      }
    >
      <Card testID="profile-name">
        <TextField
          label={t('displayName.label')}
          value={name}
          onChangeText={(v) => {
            setDraftName(v);
            setStatus('idle');
          }}
          hint={t('displayName.hint')}
          maxLength={40}
          autoCapitalize="words"
          testID="profile-display-name"
        />
        <Button
          title={t('displayName.save')}
          onPress={() => {
            const trimmed = name.trim();
            if (!trimmed) return;
            setStatus('idle');
            saveName.mutate(trimmed);
          }}
          loading={saveName.isPending}
          disabled={!name.trim() || name.trim() === (member.displayName ?? '')}
          testID="profile-save-name"
        />
      </Card>

      <ChoiceGroup
        label={t('language.heading')}
        value={lang}
        onChange={(next) => {
          setStatus('idle');
          saveLang.mutate(next);
        }}
        disabled={saveLang.isPending}
        options={languages.map((code) => ({ value: code, label: t(`language.${code}`) }))}
        testID="profile-language"
      />

      <Card testID="profile-verification">
        <Heading>{t('verification.heading')}</Heading>
        <Text tone="muted">{t(`verification.${member.verification}`)}</Text>
      </Card>

      <Card>
        <ListItem
          title={t('emergencyPlan')}
          subtitle={t('emergencyPlanHint')}
          onPress={() => router.push('/emergency-plan')}
          testID="profile-emergency-plan"
        />
        <ListItem title={t('links.privacy')} onPress={() => router.push('/privacy')} testID="profile-link-privacy" />
        <ListItem
          title={t('links.knowledge')}
          onPress={() => router.push('/knowledge')}
          testID="profile-link-knowledge"
        />
        <ListItem
          title={t('links.assistant')}
          onPress={() => router.push('/assistant')}
          testID="profile-link-assistant"
        />
      </Card>

      {status === 'saved' ? <Banner tone="success">{t('saved')}</Banner> : null}
      {status === 'failed' ? <Banner tone="danger">{t('saveFailed')}</Banner> : null}
    </Screen>
  );
}
