// The crisis help screen (ADR-0023). Rules this screen must keep:
//   - The fixed text and the phone numbers render immediately from bundled
//     locale files and the last known list; nothing here waits for the network.
//   - "Call 112" is always shown and dials tel:112 from locale copy; it does
//     not depend on the crisis phone list or the network (non-negotiable 1).
//   - "Contact a person now" is written to the outbox before it is sent, so a
//     press offline is delivered later (ADR-0018) and never lost.
//   - No animation (Screen variant "crisis").
//   - Works signed out: 112 and the numbers are shown; only the request needs
//     a session (and then links to sign-in).
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Heading, List, ListItem, Screen, Text } from '@birge/ui';
import { router } from 'expo-router';
import { useCallback, useEffect, useState } from 'react';
import { Linking } from 'react-native';

import { api } from '@/lib/api';
import { saveCrisisPhones, useCrisisPhones } from '@/lib/crisis-phones';
import { flushOutbox, outbox } from '@/lib/help-outbox';
import { useSession } from '@/lib/session';

type Source = Schemas['EscalationSource'];
type Status = 'idle' | 'sending' | 'sent' | 'queued' | 'failed';

/** LIFE_THREAT_NUMBER is fixed and independent of CRISIS_PHONES / the network. */
const LIFE_THREAT_NUMBER = '112';

const bannerFor = {
  sent: { tone: 'success', key: 'status.sent' },
  queued: { tone: 'info', key: 'status.queued' },
  failed: { tone: 'danger', key: 'status.failed' },
} as const;

/** HelpScreen shows crisis numbers and lets the member ask for a person on duty. */
export function HelpScreen({ source }: { source: Source }) {
  const { t } = useTranslation('help');
  const phones = useCrisisPhones();
  const { signedIn } = useSession();
  const [status, setStatus] = useState<Status>('idle');

  useEffect(() => {
    let active = true;
    // A request queued earlier (maybe by a previous app run) is still pending.
    void outbox.pending().then((p) => {
      if (active && p.some((e) => e.op === 'requestHumanHelp')) setStatus((s) => (s === 'idle' ? 'queued' : s));
    });
    const unsubscribe = outbox.subscribe((event) => {
      if (event.entry.op !== 'requestHumanHelp') return;
      setStatus(event.type === 'sent' ? 'sent' : event.type === 'retrying' ? 'queued' : 'failed');
    });
    return () => {
      active = false;
      unsubscribe();
    };
  }, []);

  const contact = useCallback(async () => {
    setStatus('sending');
    try {
      await outbox.enqueue('requestHumanHelp', { source });
    } catch {
      // Storage failed: send directly once rather than not at all.
      try {
        const res = await unwrap(api.POST('/me/help-requests', { body: { source } }));
        await saveCrisisPhones(res.crisisPhones);
        setStatus('sent');
      } catch {
        setStatus('failed');
      }
      return;
    }
    try {
      await flushOutbox();
    } catch {
      // Storage trouble during the flush: the entry is stored, the loop retries.
    }
    // Still pending (offline, or a flush that started before this press is
    // finishing): the request waits in the outbox.
    const pending = await outbox.pending().catch(() => []);
    if (pending.some((e) => e.op === 'requestHumanHelp')) setStatus((s) => (s === 'sending' ? 'queued' : s));
  }, [source]);

  const banner = status === 'idle' || status === 'sending' ? null : bannerFor[status];

  return (
    <Screen
      title={t('title')}
      variant="crisis"
      scroll={false}
      footer={
        signedIn ? (
          <>
            <Heading>{t('contact.heading')}</Heading>
            <Text tone="muted">{t('contact.description')}</Text>
            <Button
              title={t('contact.action')}
              variant="destructive"
              loading={status === 'sending'}
              disabled={status === 'sent'}
              onPress={contact}
              testID="help-contact"
            />
          </>
        ) : (
          <Banner
            tone="info"
            action={
              <Button
                title={t('status.signIn')}
                variant="secondary"
                onPress={() => router.push('/sign-in')}
                testID="help-sign-in"
              />
            }
          >
            {t('status.signedOut')}
          </Banner>
        )
      }
    >
      <List
        data={phones}
        keyExtractor={(p) => p.number}
        renderItem={({ item }) => (
          <ListItem
            title={item.label}
            subtitle={item.number}
            accessibilityLabel={t('call', { label: item.label, number: item.number })}
            onPress={() => void Linking.openURL(`tel:${item.number}`)}
          />
        )}
        empty={{ title: t('phonesHeading'), description: t('noPhones') }}
        header={
          <>
            <Banner
              tone="danger"
              action={
                <Button
                  title={t('call112')}
                  variant="destructive"
                  onPress={() => void Linking.openURL(`tel:${LIFE_THREAT_NUMBER}`)}
                  testID="call-112"
                />
              }
            >
              {t('lifeThreat')}
            </Banner>
            <Text variant="label">{t('intro')}</Text>
            {banner ? <Banner tone={banner.tone}>{t(banner.key)}</Banner> : null}
            {phones.length > 0 ? <Heading>{t('phonesHeading')}</Heading> : null}
          </>
        }
        testID="help-phones"
      />
    </Screen>
  );
}
