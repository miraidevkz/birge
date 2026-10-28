// Profile: view and edit the member, switch language, turn push on or off
// (iOS and Android), open related screens, sign out.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation, type Language } from '@birge/i18n';
import {
  Banner,
  Button,
  Card,
  CheckItem,
  LanguagePicker,
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
import { chooseLanguage, currentLanguage } from '@/lib/language';
import { disablePush, enablePush, isPushOn, pushSupported } from '@/lib/push';
import { useSession } from '@/lib/session';

type Member = Schemas['Member'];

/**
 * PushToggle turns push on or off for this phone: on asks for the permission
 * if needed and registers the device, off unregisters it. Only where the
 * build can receive push (lib/push.ts). A refused permission is explained
 * once; any other failure leaves the toggle off without a message (the inbox
 * works without push).
 */
function PushToggle() {
  const { t } = useTranslation('inbox');
  const [on, setOn] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [denied, setDenied] = useState(false);

  useEffect(() => {
    let active = true;
    void isPushOn().then((v) => {
      if (active) setOn(v);
    });
    return () => {
      active = false;
    };
  }, []);

  async function change(next: boolean) {
    setBusy(true);
    setDenied(false);
    try {
      if (next) {
        const result = await enablePush();
        setOn(result === 'registered');
        setDenied(result === 'denied');
      } else {
        await disablePush();
        setOn(false);
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card testID="profile-push">
      <CheckItem
        title={t('push.label')}
        checked={on === true}
        onChange={(next) => void change(next)}
        disabled={busy || on === null}
        testID="profile-push-toggle"
      />
      <Text tone="muted">{t('push.hint')}</Text>
      {denied ? <Banner tone="info">{t('push.denied')}</Banner> : null}
    </Card>
  );
}

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

  const saveName = useMutation({
    mutationFn: (displayName: string) => unwrap(api.PATCH('/me', { body: { displayName } })),
    onSuccess: (member) => {
      client.setQueryData(['me'], member);
      setDraftName(null);
      setStatus('saved');
    },
    onError: () => setStatus('failed'),
  });

  // A moderator calls her back; the member row says "pending" until then.
  const requestVerification = useMutation({
    mutationFn: () => unwrap(api.POST('/me/verification-requests')),
    onSuccess: (m) => client.setQueryData(['me'], m),
  });

  const saveLang = useMutation({
    mutationFn: (lang: Language) => unwrap(api.PATCH('/me', { body: { lang } })),
    onSuccess: (member) => {
      client.setQueryData(['me'], member);
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
  // The device's choice is what the member sees; the server copy follows it.
  const lang: Language = currentLanguage();

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

      <LanguagePicker
        value={lang}
        onChange={(next) => {
          setStatus('idle');
          // The interface follows at once; the server copy is for notifications.
          void chooseLanguage(next);
          saveLang.mutate(next);
        }}
        disabled={saveLang.isPending}
        testID="profile-language"
      />

      {pushSupported() ? <PushToggle /> : null}

      <Card testID="profile-verification">
        <Heading>{t('verification.heading')}</Heading>
        <Text tone="muted">{t(`verification.${member.verification}`)}</Text>
        {member.verification === 'new' ? (
          <>
            <Text>{t('verification.why')}</Text>
            <Button
              title={t('verification.request')}
              variant="secondary"
              loading={requestVerification.isPending}
              onPress={() => requestVerification.mutate()}
              testID="profile-verification-request"
            />
          </>
        ) : null}
        {requestVerification.isSuccess ? <Banner tone="success">{t('verification.requested')}</Banner> : null}
        {requestVerification.isError ? <Banner tone="danger">{t('verification.failed')}</Banner> : null}
      </Card>

      <Card>
        <ListItem
          title={t('emergencyPlan')}
          subtitle={t('emergencyPlanHint')}
          onPress={() => router.push('/emergency-plan')}
          testID="profile-emergency-plan"
        />
        <ListItem title={t('links.about')} onPress={() => router.push('/about-me')} testID="profile-link-about" />
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
