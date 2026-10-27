// Staff escalations queue: open / taken / closed. Contact details are revealed
// only after an explicit tap and live in component state — never in the React
// Query cache and never logged. No animation (crisis UI rule).
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ChoiceGroup, List, Screen, Sheet, Text, TextField } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useFocusEffect } from 'expo-router';
import { useCallback, useState } from 'react';

import { api } from '@/lib/api';

type Escalation = Schemas['Escalation'];
type EscalationContact = Schemas['EscalationContact'];
type Status = NonNullable<Escalation['status']>;
type Outcome = NonNullable<Escalation['outcome']>;

const STATUSES: Status[] = ['open', 'taken', 'closed'];
const OUTCOMES: Outcome[] = ['contacted', 'referred', 'emergency_services', 'no_answer', 'not_a_crisis'];

/** EscalationsScreen lists and handles SOS escalations for on-duty staff. */
export function EscalationsScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [focused, setFocused] = useState(true);
  const [status, setStatus] = useState<Status>('open');
  // Contact details stay here only: never written into the query cache.
  const [contacts, setContacts] = useState<Record<string, EscalationContact>>({});
  const [closingId, setClosingId] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Outcome>('contacted');
  const [note, setNote] = useState('');
  const [actionError, setActionError] = useState(false);

  useFocusEffect(
    useCallback(() => {
      setFocused(true);
      return () => setFocused(false);
    }, []),
  );

  const list = useQuery({
    queryKey: ['staff', 'escalations', status],
    queryFn: () =>
      unwrap(
        api.GET('/staff/escalations', {
          params: { query: { status } },
        }),
      ),
    refetchInterval: focused && status === 'open' ? 15_000 : false,
  });

  const take = useMutation({
    mutationFn: (id: string) =>
      unwrap(api.POST('/staff/escalations/{escalationId}/take', { params: { path: { escalationId: id } } })),
    onSuccess: async () => {
      setActionError(false);
      await client.invalidateQueries({ queryKey: ['staff', 'escalations'] });
    },
    onError: () => setActionError(true),
  });

  const reveal = useMutation({
    // gcTime 0: the mutation cache would otherwise keep the contact after it is hidden.
    gcTime: 0,
    mutationFn: (id: string) =>
      unwrap(
        api.POST('/staff/escalations/{escalationId}/contact', {
          params: { path: { escalationId: id } },
        }),
      ),
    onSuccess: (contact, id) => {
      setActionError(false);
      setContacts((prev) => ({ ...prev, [id]: contact }));
    },
    onError: () => setActionError(true),
  });

  const close = useMutation({
    mutationFn: (id: string) =>
      unwrap(
        api.POST('/staff/escalations/{escalationId}/close', {
          params: { path: { escalationId: id } },
          body: { outcome, note: note.trim() === '' ? undefined : note.trim().slice(0, 2000) },
        }),
      ),
    onSuccess: async (_data, id) => {
      setActionError(false);
      setClosingId(null);
      setNote('');
      setContacts((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      await client.invalidateQueries({ queryKey: ['staff', 'escalations'] });
    },
    onError: () => setActionError(true),
  });

  return (
    <Screen title={t('escalations.title')} scroll={false} variant="crisis">
      <List<Escalation>
        data={list.data?.items}
        loading={list.isLoading}
        error={list.error}
        onRetry={() => void list.refetch()}
        keyExtractor={(item) => item.id}
        empty={{ title: t('escalations.emptyTitle'), description: t('escalations.emptyDescription') }}
        testID="escalations"
        header={
          <>
            <ChoiceGroup
              label={t('escalations.status.label')}
              options={STATUSES.map((s) => ({ value: s, label: t(`escalations.status.${s}`) }))}
              value={status}
              onChange={setStatus}
              testID="escalation-status"
            />
            {actionError ? <Banner tone="danger">{t('signIn.failed')}</Banner> : null}
          </>
        }
        renderItem={({ item }) => {
          const contact = contacts[item.id];
          return (
            <Card testID={`escalation-${item.id}`}>
              <Text variant="label">{t(`escalations.source.${item.source}`)}</Text>
              <Text tone="muted">{t('escalations.openedAt', { time: item.openedAt })}</Text>
              {item.takenByMe ? <Text tone="muted">{t('escalations.takenByMe')}</Text> : null}
              {contact ? (
                <>
                  <Text testID={`contact-name-${item.id}`}>
                    {t('escalations.contactName', { name: contact.displayName })}
                  </Text>
                  <Text testID={`contact-phone-${item.id}`}>
                    {t('escalations.contactPhone', { phone: contact.phone })}
                  </Text>
                  <Text>{t('escalations.contactLang', { lang: contact.lang })}</Text>
                </>
              ) : null}
              {item.status === 'open' ? (
                <Button
                  title={t('escalations.take')}
                  onPress={() => take.mutate(item.id)}
                  loading={take.isPending && take.variables === item.id}
                  testID={`take-${item.id}`}
                />
              ) : null}
              {item.status === 'taken' && item.takenByMe ? (
                <>
                  <Button
                    title={contact ? t('escalations.hideContact') : t('escalations.revealContact')}
                    variant="secondary"
                    onPress={() => {
                      if (contact) {
                        setContacts((prev) => {
                          const next = { ...prev };
                          delete next[item.id];
                          return next;
                        });
                        return;
                      }
                      reveal.mutate(item.id);
                    }}
                    loading={reveal.isPending && reveal.variables === item.id}
                    testID={`reveal-${item.id}`}
                  />
                  <Button
                    title={t('escalations.close')}
                    onPress={() => {
                      setClosingId(item.id);
                      setOutcome('contacted');
                      setNote('');
                    }}
                    testID={`close-${item.id}`}
                  />
                </>
              ) : null}
              {item.status === 'closed' && item.outcome ? (
                <Text tone="muted">{t(`escalations.outcome.${item.outcome}`)}</Text>
              ) : null}
            </Card>
          );
        }}
      />
      <Sheet
        visible={closingId !== null}
        onClose={() => setClosingId(null)}
        title={t('escalations.closeTitle')}
        testID="close-sheet"
      >
        <ChoiceGroup
          label={t('escalations.outcome.label')}
          options={OUTCOMES.map((o) => ({ value: o, label: t(`escalations.outcome.${o}`) }))}
          value={outcome}
          onChange={setOutcome}
          testID="close-outcome"
        />
        <TextField
          label={t('escalations.note.label')}
          hint={t('escalations.note.hint')}
          value={note}
          onChangeText={setNote}
          maxLength={2000}
          testID="close-note"
        />
        <Button
          title={t('escalations.confirmClose')}
          onPress={() => {
            if (closingId) close.mutate(closingId);
          }}
          loading={close.isPending}
          testID="confirm-close"
        />
      </Sheet>
    </Screen>
  );
}
