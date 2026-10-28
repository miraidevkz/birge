// Emergency plan: child card, trusted contacts (external add/remove), send plan.
// Never suggests leaving a child with anyone except confirmed trusted contacts (N5).
import { E164, unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import {
  Banner,
  Button,
  ErrorState,
  Heading,
  Illustration,
  List,
  ListItem,
  LoadingState,
  PhoneField,
  Screen,
  Text,
  TextField,
} from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';
import { formatDateTime } from '@/lib/format';

type TrustedContact = Schemas['TrustedContact'];

/** EmergencyPlanScreen lets the member view and edit her plan and contacts. */
export function EmergencyPlanScreen() {
  const { t } = useTranslation('emergency');
  const queryClient = useQueryClient();
  const plan = useQuery({
    queryKey: ['emergency-plan'],
    queryFn: () => unwrap(api.GET('/me/emergency-plan')),
  });

  const [card, setCard] = useState<string | null>(null);
  const [name, setName] = useState('');
  const [phone, setPhone] = useState('');
  const [formError, setFormError] = useState<string | null>(null);
  const [confirmSend, setConfirmSend] = useState(false);
  // The key, not the text, so the screen can tell the sent notice apart.
  const [notice, setNotice] = useState<'childCard.saved' | 'send.sent' | 'send.needCard' | 'send.needContact' | null>(
    null,
  );

  const cardValue = card ?? plan.data?.childCard ?? '';

  const saveCard = useMutation({
    mutationFn: () => unwrap(api.PUT('/me/emergency-plan/child-card', { body: { text: cardValue } })),
    onSuccess: async () => {
      setNotice('childCard.saved');
      await queryClient.invalidateQueries({ queryKey: ['emergency-plan'] });
    },
  });

  const addContact = useMutation({
    mutationFn: (body: { kind: 'external'; name: string; phone: string }) =>
      unwrap(api.POST('/me/trusted-contacts', { body })),
    onSuccess: async () => {
      setName('');
      setPhone('');
      setFormError(null);
      await queryClient.invalidateQueries({ queryKey: ['emergency-plan'] });
    },
  });

  const removeContact = useMutation({
    mutationFn: (contactId: string) =>
      unwrap(api.DELETE('/me/trusted-contacts/{contactId}', { params: { path: { contactId } } })),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['emergency-plan'] });
    },
  });

  const sendPlan = useMutation({
    mutationFn: () => unwrap(api.POST('/me/emergency-plan/send', { body: { confirm: 'SEND' } })),
    onSuccess: async () => {
      setConfirmSend(false);
      setNotice('send.sent');
      await queryClient.invalidateQueries({ queryKey: ['emergency-plan'] });
    },
  });

  if (plan.isLoading && !plan.data) {
    return (
      <Screen title={t('title')}>
        <LoadingState />
      </Screen>
    );
  }

  if (plan.error && !plan.data) {
    return (
      <Screen title={t('title')}>
        <ErrorState title={t('loadError')} onRetry={() => void plan.refetch()} />
      </Screen>
    );
  }

  const contacts = plan.data?.contacts ?? [];
  const confirmed = contacts.filter((c) => c.status === 'confirmed');

  function onAdd() {
    const trimmed = name.trim();
    if (!trimmed) {
      setFormError(t('contacts.invalidName'));
      return;
    }
    if (!E164.test(phone)) {
      setFormError(t('contacts.invalidPhone'));
      return;
    }
    if (contacts.length >= 3) {
      setFormError(t('contacts.full'));
      return;
    }
    setFormError(null);
    addContact.mutate({ kind: 'external', name: trimmed, phone });
  }

  function onSend() {
    if (!cardValue.trim()) {
      setNotice('send.needCard');
      return;
    }
    if (confirmed.length === 0) {
      setNotice('send.needContact');
      return;
    }
    setConfirmSend(true);
  }

  return (
    <Screen
      title={t('title')}
      scroll={false}
      testID="emergency-plan"
      // Pinned: only the one primary action. The add-contact form scrolls with
      // the content: in the footer it took most of a phone screen.
      footer={
        <>
          {confirmSend ? (
            <>
              <Banner tone="warning">{t('send.confirm')}</Banner>
              <Button
                title={t('send.confirmAction')}
                onPress={() => sendPlan.mutate()}
                loading={sendPlan.isPending}
                testID="emergency-send-confirm"
              />
              <Button title={t('send.cancel')} variant="secondary" onPress={() => setConfirmSend(false)} />
            </>
          ) : (
            <Button title={t('send.action')} onPress={onSend} testID="emergency-send" />
          )}
        </>
      }
    >
      <List<TrustedContact>
        data={contacts}
        loading={false}
        error={plan.error}
        onRetry={() => void plan.refetch()}
        keyExtractor={(c) => c.id}
        renderItem={({ item }) => (
          <ListItem
            title={item.displayName}
            subtitle={`${t(`contacts.kind.${item.kind}`)}, ${t(`contacts.status.${item.status}`)}${
              item.phoneMasked ? `, ${item.phoneMasked}` : ''
            }`}
            trailing={
              <Button
                title={t('contacts.remove')}
                variant="secondary"
                size="sm"
                loading={removeContact.isPending}
                onPress={() => removeContact.mutate(item.id)}
                testID={`emergency-remove-${item.id}`}
              />
            }
          />
        )}
        footer={
          <>
            <Heading>{t('contacts.addHeading')}</Heading>
            <TextField
              label={t('contacts.name')}
              value={name}
              onChangeText={setName}
              maxLength={60}
              testID="emergency-name"
            />
            <PhoneField
              label={t('contacts.phone')}
              hint={t('contacts.phoneHint')}
              error={formError ?? undefined}
              value={phone}
              onChangePhone={setPhone}
              testID="emergency-phone"
            />
            <Button
              title={t('contacts.add')}
              variant="secondary"
              loading={addContact.isPending}
              onPress={onAdd}
              testID="emergency-add"
            />
            <Heading>{t('send.heading')}</Heading>
            <Text tone="muted">{t('send.description')}</Text>
          </>
        }
        empty={{ title: t('contacts.emptyTitle'), description: t('contacts.emptyDescription') }}
        header={
          <>
            <Text>{t('intro')}</Text>
            {notice === 'send.sent' ? <Illustration name="done" size="sm" /> : null}
            {notice ? <Banner tone="info">{t(notice)}</Banner> : null}
            {saveCard.isError || addContact.isError || sendPlan.isError || removeContact.isError ? (
              <Banner tone="danger">{t('saveError')}</Banner>
            ) : null}
            {plan.data?.lastSentAt ? (
              <Text tone="muted">{t('lastSent', { when: formatDateTime(plan.data.lastSentAt, i18n.language) })}</Text>
            ) : null}
            <Heading>{t('childCard.heading')}</Heading>
            <TextField
              label={t('childCard.label')}
              hint={t('childCard.hint')}
              value={cardValue}
              onChangeText={(v) => {
                setCard(v);
                setNotice(null);
              }}
              multiline
              maxLength={2000}
              testID="emergency-child-card"
            />
            <Button
              title={t('childCard.save')}
              onPress={() => saveCard.mutate()}
              loading={saveCard.isPending}
              testID="emergency-save-card"
            />
            <Heading>{t('contacts.heading')}</Heading>
          </>
        }
        testID="emergency-contacts"
      />
    </Screen>
  );
}
