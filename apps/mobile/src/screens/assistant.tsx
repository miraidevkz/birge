// AI assistant chat. Crisis replies show the fixed help text, "call 112" and
// the crisis phones in the reply itself (ADR-0023), plus a Help link;
// reaching Help never depends on this screen (SOS is on every Screen; Help is /sos).
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation, type Language } from '@birge/i18n';
import {
  Banner,
  Button,
  CallItem,
  Card,
  ChoiceGroup,
  LifeThreatBanner,
  List,
  ListItem,
  Screen,
  Text,
  TextField,
} from '@birge/ui';
import { router } from 'expo-router';
import { useCallback, useState } from 'react';

import { api } from '@/lib/api';
import { saveCrisisPhones, useCrisisPhones } from '@/lib/crisis-phones';
import { flushOutbox, outbox } from '@/lib/help-outbox';

type Scope = Schemas['AssistantMessageInput']['scope'];

type ChatMessage = {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  crisis?: boolean;
  degraded?: boolean;
};

function lang(): Language {
  return (i18n.language === 'ru' || i18n.language === 'en' ? i18n.language : 'kk') as Language;
}

function errorCode(err: unknown): 'consent_required' | 'daily_limit' | 'unknown' {
  if (err instanceof ApiError) {
    if (err.code === 'consent_required') return 'consent_required';
    if (err.code === 'daily_limit') return 'daily_limit';
  }
  return 'unknown';
}

/** AssistantScreen is the member-facing AI chat. */
export function AssistantScreen() {
  const { t } = useTranslation('assistant');
  const { t: tHelp } = useTranslation('help');
  const [scope, setScope] = useState<Scope>('general');
  const [draft, setDraft] = useState('');
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<'consent_required' | 'daily_limit' | 'unknown' | null>(null);
  const [remaining, setRemaining] = useState<number | null>(null);
  const [crisisActive, setCrisisActive] = useState(false);
  const phones = useCrisisPhones();
  const [contactStatus, setContactStatus] = useState<'idle' | 'sending' | 'queued' | 'sent' | 'failed'>('idle');

  const contactPerson = useCallback(async () => {
    setContactStatus('sending');
    try {
      await outbox.enqueue('requestHumanHelp', { source: 'assistant' });
    } catch {
      try {
        await unwrap(api.POST('/me/help-requests', { body: { source: 'assistant' } }));
        setContactStatus('sent');
      } catch {
        setContactStatus('failed');
      }
      return;
    }
    try {
      await flushOutbox();
    } catch {
      // Entry stays queued; the replay loop retries.
    }
    const pending = await outbox.pending().catch(() => []);
    if (pending.some((e) => e.op === 'requestHumanHelp')) {
      setContactStatus((s) => (s === 'sending' ? 'queued' : s));
    } else {
      setContactStatus((s) => (s === 'sending' ? 'sent' : s));
    }
  }, []);

  async function send() {
    const text = draft.trim();
    if (!text || busy) return;
    setBusy(true);
    setError(null);
    const userMsg: ChatMessage = { id: `u-${Date.now()}`, role: 'user', text };
    setMessages((prev) => [...prev, userMsg]);
    setDraft('');
    try {
      const reply = await unwrap(
        api.POST('/me/assistant/messages', {
          body: { text, scope, lang: lang() },
        }),
      );
      if (reply.remainingToday !== undefined) setRemaining(reply.remainingToday);
      if (reply.crisis) {
        setCrisisActive(true);
        if (reply.crisisPhones) await saveCrisisPhones(reply.crisisPhones);
        setMessages((prev) => [
          ...prev,
          {
            id: reply.messageId,
            role: 'assistant',
            text: tHelp('intro'),
            crisis: true,
          },
        ]);
      } else {
        setMessages((prev) => [
          ...prev,
          {
            id: reply.messageId,
            role: 'assistant',
            text: reply.text ?? '',
            degraded: reply.degraded,
          },
        ]);
      }
    } catch (err) {
      setError(errorCode(err));
      // Keep the draft so the member can retry without retyping.
      setDraft(text);
      setMessages((prev) => prev.filter((m) => m.id !== userMsg.id));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Screen
      title={t('title')}
      scroll={false}
      footer={
        crisisActive ? (
          <Button
            title={t('crisis.openHelp')}
            variant="destructive"
            onPress={() => router.push('/sos')}
            testID="assistant-open-help"
          />
        ) : (
          <>
            <TextField
              label={t('placeholder')}
              value={draft}
              onChangeText={setDraft}
              editable={!busy}
              maxLength={2000}
              multiline
              testID="assistant-input"
            />
            <Button
              title={t('send')}
              onPress={() => void send()}
              loading={busy}
              disabled={!draft.trim()}
              testID="assistant-send"
            />
          </>
        )
      }
    >
      <List<ChatMessage>
        data={messages}
        keyExtractor={(m) => m.id}
        renderItem={({ item }) => (
          <Card testID={`assistant-msg-${item.id}`}>
            <ListItem
              title={item.crisis ? t('crisis.heading') : item.role === 'user' ? t('you') : t('assistant')}
              subtitle={item.text}
              emphasis={item.crisis === true}
            />
            {item.crisis ? (
              <>
                <LifeThreatBanner message={tHelp('lifeThreat')} callTitle={tHelp('call112')} />
                {phones.map((p) => (
                  <CallItem
                    key={p.number}
                    label={p.label}
                    number={p.number}
                    accessibilityLabel={tHelp('call', { label: p.label, number: p.number })}
                  />
                ))}
              </>
            ) : null}
            {item.degraded ? (
              <Banner
                tone="warning"
                action={
                  <Button
                    title={t('degraded.contact')}
                    variant="secondary"
                    size="sm"
                    loading={contactStatus === 'sending'}
                    disabled={contactStatus === 'sent'}
                    onPress={() => void contactPerson()}
                    testID="assistant-contact"
                  />
                }
              >
                {t('degraded.body')}
              </Banner>
            ) : null}
          </Card>
        )}
        empty={{ title: t('emptyTitle'), description: t('emptyDescription') }}
        header={
          <>
            <Text>{t('intro')}</Text>
            <ChoiceGroup
              label={t('scope.heading')}
              value={scope}
              onChange={setScope}
              disabled={busy || crisisActive}
              options={[
                { value: 'general', label: t('scope.general') },
                { value: 'rights', label: t('scope.rights') },
              ]}
              testID="assistant-scope"
            />
            {remaining !== null ? <Text tone="muted">{t('remaining', { count: remaining })}</Text> : null}
            {error === 'consent_required' ? (
              <Banner
                tone="warning"
                action={
                  <Button
                    title={t('openPrivacy')}
                    variant="secondary"
                    size="sm"
                    onPress={() => router.push('/privacy')}
                    testID="assistant-open-privacy"
                  />
                }
              >
                {t('errors.consent_required')}
              </Banner>
            ) : null}
            {error === 'daily_limit' ? <Banner tone="warning">{t('errors.daily_limit')}</Banner> : null}
            {error === 'unknown' ? (
              <Banner
                tone="danger"
                action={
                  <Button
                    title={t('crisis.openHelp')}
                    variant="secondary"
                    size="sm"
                    onPress={() => router.push('/sos')}
                    testID="assistant-error-help"
                  />
                }
              >
                {t('errors.unknown')}
              </Banner>
            ) : null}
            {contactStatus === 'queued' ? <Banner tone="info">{tHelp('status.queued')}</Banner> : null}
            {contactStatus === 'sent' ? <Banner tone="success">{tHelp('status.sent')}</Banner> : null}
            {contactStatus === 'failed' ? <Banner tone="danger">{tHelp('status.failed')}</Banner> : null}
          </>
        }
        testID="assistant-messages"
      />
    </Screen>
  );
}
