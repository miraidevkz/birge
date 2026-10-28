// First-run setup after the first sign-in (owner, 2026-10-28): three short
// cards — her name, her district and child, verification. Everything can be
// skipped and changed later in Profile and About me; Home keeps inviting her
// while the district is unset. Shown while she has no display name and has
// not finished or skipped it on this device.
import { unwrap } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, ErrorState, Heading, Illustration, LoadingState, Screen, Text, TextField } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';
import { storage } from '@/lib/storage';

import { AboutMeSections } from './about-me';

/** SETUP_KEY marks that this device finished or skipped the first-run setup. */
export const SETUP_KEY = 'birge.setupDone';

/** needsSetup reports whether to show the setup: no name yet and not done here. */
export async function needsSetup(displayName: string | undefined): Promise<boolean> {
  if (displayName && displayName.trim() !== '') return false;
  try {
    return (await storage.get(SETUP_KEY)) !== '1';
  } catch {
    return false;
  }
}

const STEPS = ['name', 'about', 'verify'] as const;

/** SetupScreenProps: where to go when she is done or skips. */
export interface SetupScreenProps {
  onDone: () => void;
}

/** SetupScreen walks through the three setup cards. */
export function SetupScreen({ onDone }: SetupScreenProps) {
  const { t } = useTranslation('setup');
  const client = useQueryClient();
  const me = useQuery({ queryKey: ['me'], queryFn: () => unwrap(api.GET('/me')) });
  const [step, setStep] = useState(0);
  const [name, setName] = useState('');
  const key = STEPS[step] ?? 'name';
  const last = step === STEPS.length - 1;

  const saveName = useMutation({
    mutationFn: (displayName: string) => unwrap(api.PATCH('/me', { body: { displayName } })),
    onSuccess: (m) => {
      client.setQueryData(['me'], m);
      setStep(1);
    },
  });
  const verify = useMutation({
    mutationFn: () => unwrap(api.POST('/me/verification-requests')),
    onSuccess: (m) => client.setQueryData(['me'], m),
  });

  async function finish() {
    try {
      await storage.set(SETUP_KEY, '1');
    } catch {
      // Not remembered: she may see the setup once more, nothing worse.
    }
    onDone();
  }

  function next() {
    if (key === 'name') {
      const trimmed = name.trim();
      if (trimmed) saveName.mutate(trimmed);
      else setStep(1);
      return;
    }
    if (last) void finish();
    else setStep(step + 1);
  }

  if (me.isLoading) {
    return (
      <Screen title={t('title')}>
        <LoadingState />
      </Screen>
    );
  }
  if (!me.data) {
    return (
      <Screen title={t('title')}>
        <ErrorState onRetry={() => void me.refetch()} />
      </Screen>
    );
  }
  const member = me.data;
  const requested = verify.isSuccess || member.verification !== 'new';

  return (
    <Screen
      title={t(`${key}.title`)}
      transitionKey={key}
      testID={`setup-${key}`}
      footer={
        <>
          <Button
            title={last ? t('finish') : t('next')}
            onPress={next}
            loading={saveName.isPending}
            testID="setup-next"
          />
          {last ? null : (
            <Button title={t('skip')} variant="secondary" onPress={() => void finish()} testID="setup-skip" />
          )}
        </>
      }
    >
      <Text tone="muted">{t('progress', { step: step + 1, total: STEPS.length })}</Text>
      <Text>{t(`${key}.body`)}</Text>
      {key === 'name' ? (
        <TextField
          label={t('name.label')}
          hint={t('name.hint')}
          value={name}
          onChangeText={setName}
          maxLength={40}
          autoComplete="given-name"
          testID="setup-name-input"
        />
      ) : null}
      {saveName.isError ? <Banner tone="danger">{t('failed')}</Banner> : null}
      {key === 'about' ? <AboutMeSections member={member} /> : null}
      {key === 'verify' ? (
        <>
          <Illustration name="privacy" size="sm" />
          {requested ? (
            <Banner tone="success">{t('verify.requested')}</Banner>
          ) : (
            <Button
              title={t('verify.request')}
              variant="secondary"
              loading={verify.isPending}
              onPress={() => verify.mutate()}
              testID="setup-verify-request"
            />
          )}
          {verify.isError ? <Banner tone="danger">{t('failed')}</Banner> : null}
          <Heading>{t('verify.afterHeading')}</Heading>
          <Text tone="muted">{t('verify.after')}</Text>
        </>
      ) : null}
    </Screen>
  );
}
