// SOS screen (crisis variant, ADR-0014): phones render at once from the last
// known list; raising/closing uses the API; "I feel very bad" is requestHumanHelp
// with source sos (not POST /me/sos). Never blocks on the network for phones.
// "Call 112" is always shown from the help locale and dials tel:112, independent
// of the crisis phone list (mvp §4.6, non-negotiable 1).
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import {
  Banner,
  Button,
  CallItem,
  Heading,
  LifeThreatBanner,
  List,
  ListItem,
  Screen,
  Text,
  TextField,
} from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useCallback, useEffect, useState } from 'react';

import { api } from '@/lib/api';
import { saveCrisisPhones, useCrisisPhones } from '@/lib/crisis-phones';
import { flushOutbox, outbox } from '@/lib/help-outbox';
import { useSession } from '@/lib/session';

type SosRequest = Schemas['SosRequest'];
type SosType = Schemas['SosType'];
type DraftType = SosType | null;
type HelpStatus = 'idle' | 'sending' | 'sent' | 'queued' | 'failed';

/** SosScreen raises and closes SOS and shows crisis phones immediately. */
export function SosScreen() {
  const { t } = useTranslation('sos');
  const { t: tHelp } = useTranslation('help');
  const phones = useCrisisPhones();
  const { signedIn } = useSession();
  const queryClient = useQueryClient();

  const [draft, setDraft] = useState<DraftType>(null);
  const [text, setText] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [banner, setBanner] = useState<{ tone: 'success' | 'info' | 'danger' | 'warning'; key: string } | null>(null);
  const [helpStatus, setHelpStatus] = useState<HelpStatus>('idle');

  const list = useQuery({
    queryKey: ['sos', 'mine'],
    queryFn: () => unwrap(api.GET('/me/sos')),
    enabled: signedIn,
  });

  const open = (list.data ?? []).filter((s) => s.status === 'open');

  const raise = useMutation({
    mutationFn: (body: { type: SosType; text: string }) => unwrap(api.POST('/me/sos', { body })),
    onSuccess: async () => {
      setDraft(null);
      setText('');
      setBanner({ tone: 'success', key: 'status.raised' });
      await queryClient.invalidateQueries({ queryKey: ['sos', 'mine'] });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 429) {
        setBanner({ tone: 'warning', key: 'status.rateLimited' });
        return;
      }
      setBanner({ tone: 'danger', key: 'status.failed' });
    },
  });

  const close = useMutation({
    mutationFn: (sosId: string) => unwrap(api.POST('/me/sos/{sosId}/close', { params: { path: { sosId } } })),
    onSuccess: async () => {
      setBanner({ tone: 'success', key: 'status.closed' });
      await queryClient.invalidateQueries({ queryKey: ['sos', 'mine'] });
    },
    onError: () => setBanner({ tone: 'danger', key: 'status.failed' }),
  });

  useEffect(() => {
    let active = true;
    void outbox.pending().then((p) => {
      if (active && p.some((e) => e.op === 'requestHumanHelp')) {
        setHelpStatus((s) => (s === 'idle' ? 'queued' : s));
      }
    });
    const unsubscribe = outbox.subscribe((event) => {
      if (event.entry.op !== 'requestHumanHelp') return;
      setHelpStatus(event.type === 'sent' ? 'sent' : event.type === 'retrying' ? 'queued' : 'failed');
      if (event.type === 'sent') setBanner({ tone: 'success', key: 'status.sentHelp' });
      if (event.type === 'dropped') setBanner({ tone: 'danger', key: 'status.failed' });
      if (event.type === 'retrying') setBanner({ tone: 'info', key: 'status.queued' });
    });
    return () => {
      active = false;
      unsubscribe();
    };
  }, []);

  const contactPerson = useCallback(async () => {
    if (!signedIn) {
      setBanner({ tone: 'info', key: 'status.signedOut' });
      return;
    }
    setHelpStatus('sending');
    try {
      await outbox.enqueue('requestHumanHelp', { source: 'sos' });
    } catch {
      try {
        const res = await unwrap(api.POST('/me/help-requests', { body: { source: 'sos' } }));
        await saveCrisisPhones(res.crisisPhones);
        setHelpStatus('sent');
        setBanner({ tone: 'success', key: 'status.sentHelp' });
      } catch {
        setHelpStatus('failed');
        setBanner({ tone: 'danger', key: 'status.failed' });
      }
      return;
    }
    try {
      await flushOutbox();
    } catch {
      // Stored; replay retries.
    }
    const pending = await outbox.pending().catch(() => []);
    if (pending.some((e) => e.op === 'requestHumanHelp')) {
      setHelpStatus((s) => (s === 'sending' ? 'queued' : s));
      setBanner({ tone: 'info', key: 'status.queued' });
    } else {
      setHelpStatus((s) => (s === 'sending' ? 'sent' : s));
      setBanner({ tone: 'success', key: 'status.sentHelp' });
    }
  }, [signedIn]);

  function onRaise() {
    if (!signedIn || !draft) {
      setBanner({ tone: 'info', key: 'status.signedOut' });
      return;
    }
    const trimmed = text.trim();
    if (!trimmed) {
      setFormError(t('form.empty'));
      return;
    }
    setFormError(null);
    raise.mutate({ type: draft, text: trimmed.slice(0, 300) });
  }

  const signedOutFooter = (
    <Banner
      tone="info"
      action={
        <Button
          title={tHelp('status.signIn')}
          variant="secondary"
          onPress={() => router.push('/sign-in')}
          testID="sos-sign-in"
        />
      }
    >
      {t('status.signedOut')}
    </Banner>
  );

  const requestFooter = draft ? (
    <>
      <Heading>{t('form.heading')}</Heading>
      {draft === 'childcare' ? <Text tone="muted">{t('form.childcareNote')}</Text> : null}
      <TextField
        label={t('form.label')}
        hint={t('form.hint')}
        error={formError ?? undefined}
        value={text}
        onChangeText={setText}
        maxLength={300}
        multiline
        testID="sos-text"
      />
      <Button
        title={t('form.action')}
        variant="destructive"
        loading={raise.isPending}
        onPress={onRaise}
        testID="sos-raise"
      />
      <Button
        title={t('form.cancel')}
        variant="secondary"
        onPress={() => {
          setDraft(null);
          setText('');
          setFormError(null);
        }}
      />
    </>
  ) : (
    <>
      <Heading>{t('types.heading')}</Heading>
      <Button
        title={t('types.childcare')}
        variant="secondary"
        onPress={() => setDraft('childcare')}
        testID="sos-type-childcare"
      />
      <Button
        title={t('types.feelBad')}
        variant="destructive"
        loading={helpStatus === 'sending'}
        disabled={helpStatus === 'sent'}
        onPress={contactPerson}
        testID="sos-type-feel-bad"
      />
      <Button
        title={t('types.item')}
        variant="secondary"
        onPress={() => setDraft('item_or_medicine')}
        testID="sos-type-item"
      />
      <Button title={t('types.other')} variant="secondary" onPress={() => setDraft('other')} testID="sos-type-other" />
    </>
  );

  return (
    <Screen
      title={t('title')}
      variant="crisis"
      scroll={false}
      testID="sos-screen"
      footer={signedIn ? requestFooter : signedOutFooter}
    >
      <List
        data={phones}
        keyExtractor={(p) => p.number}
        renderItem={({ item }) => (
          <CallItem
            label={item.label}
            number={item.number}
            accessibilityLabel={t('call', { label: item.label, number: item.number })}
          />
        )}
        empty={{ title: t('phonesHeading'), description: t('noPhones') }}
        header={
          <>
            <LifeThreatBanner message={tHelp('lifeThreat')} callTitle={tHelp('call112')} />
            {banner ? <Banner tone={banner.tone}>{t(banner.key)}</Banner> : null}
            {phones.length > 0 ? <Heading>{t('phonesHeading')}</Heading> : null}
            {signedIn && open.length > 0 ? (
              <>
                <Heading>{t('open.heading')}</Heading>
                {open.map((sos: SosRequest) => (
                  <ListItem
                    key={sos.id}
                    title={t(`open.type.${sos.type}`)}
                    subtitle={sos.text}
                    trailing={
                      <Button
                        title={t('open.close')}
                        variant="secondary"
                        size="sm"
                        loading={close.isPending}
                        onPress={() => close.mutate(sos.id)}
                        testID={`sos-close-${sos.id}`}
                      />
                    }
                  />
                ))}
              </>
            ) : null}
          </>
        }
        testID="sos-phones"
      />
    </Screen>
  );
}
