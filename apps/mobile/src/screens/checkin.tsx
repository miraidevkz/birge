// Weekly well-being check-in: start, one question per step, submit, result.
// Crisis flag reuses HelpScreen (fixed text + phones, source checkin). Elevated
// offers requestHumanHelp via the outbox. Never a diagnosis.
import { unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Banner, Button, ErrorState, Heading, Illustration, LoadingState, Screen, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useCallback, useState } from 'react';

import { api } from '@/lib/api';
import { saveCrisisPhones } from '@/lib/crisis-phones';
import { flushOutbox, outbox } from '@/lib/help-outbox';
import { wellbeingKey } from '@/lib/wellbeing-text';

import { HelpScreen } from './help';

type Questionnaire = Schemas['Questionnaire'];
type Question = Questionnaire['questions'][number];
type CheckinResult = Schemas['CheckinResult'];
type Answer = { questionId: string; value: number };

type Step = 'start' | 'questions' | 'result';

function translateKey(raw: string, fallback: string): string {
  const key = wellbeingKey(raw);
  return i18n.exists(key, { ns: 'wellbeing' }) ? i18n.t(key, { ns: 'wellbeing' }) : fallback;
}

/** CheckinScreen is the weekly How-am-I flow. */
export function CheckinScreen() {
  const { t } = useTranslation('wellbeing');
  const queryClient = useQueryClient();
  const [step, setStep] = useState<Step>('start');
  const [index, setIndex] = useState(0);
  const [answers, setAnswers] = useState<Answer[]>([]);
  const [result, setResult] = useState<CheckinResult | null>(null);
  const [helpStatus, setHelpStatus] = useState<'idle' | 'sending' | 'sent' | 'queued' | 'failed'>('idle');

  const questionnaire = useQuery({
    queryKey: ['checkins', 'questionnaire'],
    queryFn: () => unwrap(api.GET('/me/checkins/questionnaire')),
  });

  const submit = useMutation({
    mutationFn: async (body: { questionnaireVersion: string; answers: Answer[] }) =>
      unwrap(api.POST('/me/checkins', { body })),
    onSuccess: async (res) => {
      if (res.crisisPhones) await saveCrisisPhones(res.crisisPhones);
      setResult(res);
      setStep('result');
      void queryClient.invalidateQueries({ queryKey: ['checkins', 'history'] });
    },
  });

  const questions = questionnaire.data?.questions ?? [];
  const current: Question | undefined = questions[index];

  const answer = useCallback(
    (value: number) => {
      if (!questionnaire.data || !current) return;
      const nextAnswers = [...answers.filter((a) => a.questionId !== current.id), { questionId: current.id, value }];
      setAnswers(nextAnswers);
      if (index + 1 < questions.length) {
        setIndex(index + 1);
        return;
      }
      submit.mutate({ questionnaireVersion: questionnaire.data.version, answers: nextAnswers });
    },
    [answers, current, index, questionnaire.data, questions.length, submit],
  );

  const contactPsychologist = useCallback(async () => {
    setHelpStatus('sending');
    try {
      await outbox.enqueue('requestHumanHelp', { source: 'checkin' });
    } catch {
      try {
        const res = await unwrap(api.POST('/me/help-requests', { body: { source: 'checkin' } }));
        await saveCrisisPhones(res.crisisPhones);
        setHelpStatus('sent');
      } catch {
        setHelpStatus('failed');
      }
      return;
    }
    try {
      await flushOutbox();
    } catch {
      // Entry is stored; the replay loop retries.
    }
    const pending = await outbox.pending().catch(() => []);
    if (pending.some((e) => e.op === 'requestHumanHelp')) {
      setHelpStatus((s) => (s === 'sending' ? 'queued' : s));
    } else {
      setHelpStatus((s) => (s === 'sending' ? 'sent' : s));
    }
  }, []);

  if (result?.flag === 'crisis') {
    return <HelpScreen source="checkin" />;
  }

  if (step === 'start') {
    return (
      <Screen
        title={t('title')}
        footer={
          <>
            <Button
              title={t('start.action')}
              onPress={() => {
                setStep('questions');
                setIndex(0);
                setAnswers([]);
                setResult(null);
                submit.reset();
              }}
              disabled={questionnaire.isLoading || !!questionnaire.error || questions.length === 0}
              testID="checkin-start"
            />
            <Button
              title={t('start.history')}
              variant="secondary"
              onPress={() => router.push('/checkin-history')}
              testID="checkin-open-history"
            />
          </>
        }
      >
        <Heading>{t('start.heading')}</Heading>
        <Text>{t('start.intro')}</Text>
        {questionnaire.isLoading ? <LoadingState /> : null}
        {questionnaire.error ? <ErrorState onRetry={() => void questionnaire.refetch()} /> : null}
      </Screen>
    );
  }

  if (step === 'questions') {
    if (questionnaire.isLoading && !questionnaire.data) {
      return (
        <Screen title={t('title')}>
          <LoadingState />
        </Screen>
      );
    }
    if (questionnaire.error || !current) {
      return (
        <Screen title={t('title')}>
          <ErrorState onRetry={() => void questionnaire.refetch()} />
        </Screen>
      );
    }
    return (
      <Screen title={t('title')} testID="checkin-question">
        <Text variant="caption" tone="muted">
          {t('progress', { current: index + 1, total: questions.length })}
        </Text>
        <Heading>{translateKey(current.textKey, t('questions.missing'))}</Heading>
        {submit.isError ? <Banner tone="danger">{t('submitError')}</Banner> : null}
        {current.options.map((opt) => (
          <Button
            key={`${current.id}-${opt.value}`}
            title={translateKey(opt.textKey, String(opt.value))}
            variant="secondary"
            loading={submit.isPending}
            onPress={() => answer(opt.value)}
            testID={`checkin-option-${opt.value}`}
          />
        ))}
      </Screen>
    );
  }

  // Result (non-crisis).
  const feedback = result ? translateKey(result.feedbackKey, t('feedback.missing')) : t('feedback.missing');
  const suggestionKey = result ? `suggestion.${result.suggestion}` : 'suggestion.none';

  return (
    <Screen
      title={t('result.title')}
      footer={
        <>
          <Button title={t('result.done')} onPress={() => router.back()} testID="checkin-done" />
          <Button title={t('result.history')} variant="secondary" onPress={() => router.push('/checkin-history')} />
        </>
      }
      testID="checkin-result"
    >
      {/* A check mark only for a calm result: on "elevated" it would read as "all is well". */}
      {result?.flag === 'none' ? <Illustration name="done" size="sm" /> : null}
      <Text>{feedback}</Text>
      <Heading>{t('suggestion.heading')}</Heading>
      <Text tone="muted">{t(suggestionKey)}</Text>
      {result?.flag === 'elevated' ? (
        <>
          <Heading>{t('elevated.heading')}</Heading>
          <Text tone="muted">{t('elevated.description')}</Text>
          {helpStatus === 'sent' ? <Banner tone="success">{t('elevated.sent')}</Banner> : null}
          {helpStatus === 'queued' ? <Banner tone="info">{t('elevated.queued')}</Banner> : null}
          {helpStatus === 'failed' ? <Banner tone="danger">{t('elevated.failed')}</Banner> : null}
          <Button
            title={t('elevated.action')}
            variant="destructive"
            loading={helpStatus === 'sending'}
            disabled={helpStatus === 'sent'}
            onPress={contactPsychologist}
            testID="checkin-psychologist"
          />
        </>
      ) : null}
    </Screen>
  );
}
