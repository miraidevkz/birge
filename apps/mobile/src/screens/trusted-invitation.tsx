// An invitation to be another member's trusted contact (contract:
// getTrustedInvitation, `GET /me/trusted-invitations/{contactId}`;
// acceptTrustedInvitation and declineTrustedInvitation, `POST …/accept` and
// `POST …/decline`). Opened from the `emergency.trusted_invitation` inbox
// item, whose only param is the contact id.
//
// The invitation names who invited her by first name. While it is open she
// may accept, decline (with a confirmation) or say "Not now", which only
// leaves the screen and keeps the invitation open. Once she has accepted, the
// screen says so and offers to stop being her trusted contact: the same
// decline operation, also confirmed. Declining is the owner's removal, without
// a notification or a reason (ADR-0023 addendum 2026-10-28).
//
// The server hides a removed invitation, one not addressed to her and a block
// behind 404 ("no longer active"); accept re-checks the meeting in person
// (403). Both are final for this screen. Other failures keep the screen with
// a retry.
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, ConfirmSheet, ErrorState, Illustration, LoadingState, Screen, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';
import { goBack } from '@/lib/nav';
import { isUuid } from '@/lib/notification-targets';

type Invitation = Schemas['TrustedInvitation'];
type Refusal = 'gone' | 'unmet' | null;

/** refusal tells a final answer from the server apart from one worth retrying. */
function refusal(err: unknown): Refusal {
  if (!(err instanceof ApiError)) return null;
  if (err.status === 404) return 'gone';
  if (err.status === 403) return 'unmet';
  return null;
}

/** trustedInvitationKey is the query key of one invitation addressed to her. */
export const trustedInvitationKey = (contactId: string) => ['emergency', 'trusted-invitation', contactId] as const;

/** TrustedInvitationScreen lets her accept or decline being another member's trusted contact, or stop being one. */
export function TrustedInvitationScreen({ contactId }: { contactId: string }) {
  const { t } = useTranslation('emergency');
  const queryClient = useQueryClient();
  const valid = isUuid(contactId);
  const [confirming, setConfirming] = useState(false);
  const key = trustedInvitationKey(contactId);

  const invitation = useQuery({
    queryKey: key,
    queryFn: () => unwrap(api.GET('/me/trusted-invitations/{contactId}', { params: { path: { contactId } } })),
    enabled: valid,
  });
  const accept = useMutation({
    mutationFn: () =>
      unwrap(api.POST('/me/trusted-invitations/{contactId}/accept', { params: { path: { contactId } } })),
    onSuccess: () => {
      queryClient.setQueryData<Invitation>(key, (old) => (old ? { ...old, status: 'confirmed' } : old));
    },
  });
  const decline = useMutation({
    mutationFn: () =>
      unwrap(api.POST('/me/trusted-invitations/{contactId}/decline', { params: { path: { contactId } } })),
    onSuccess: () => {
      // The entry is gone; a later visit reads it again and gets 404. No
      // refetch now: the screen shows the result of her own action.
      void queryClient.invalidateQueries({ queryKey: key, refetchType: 'none' });
    },
    onSettled: () => setConfirming(false),
  });

  const title = t('invitation.title');
  const busy = accept.isPending || decline.isPending;
  const final: Refusal =
    !valid || (invitation.isError && refusal(invitation.error) === 'gone')
      ? 'gone'
      : (refusal(accept.error) ?? (refusal(decline.error) === 'gone' ? 'gone' : null));

  if (decline.isSuccess) {
    const wasConfirmed = invitation.data?.status === 'confirmed';
    return (
      <Screen
        title={title}
        testID="trusted-invitation"
        footer={<Button title={t('invitation.done')} onPress={goBack} testID="trusted-invitation-done" />}
      >
        <Text>{t(wasConfirmed ? 'invitation.left' : 'invitation.declined')}</Text>
      </Screen>
    );
  }
  if (final === 'gone') {
    return (
      <Screen title={title} testID="trusted-invitation">
        <ErrorState title={t('invitation.goneTitle')} description={t('invitation.goneDescription')} />
      </Screen>
    );
  }
  if (final === 'unmet') {
    return (
      <Screen title={title} testID="trusted-invitation">
        <ErrorState title={t('invitation.unmetTitle')} description={t('invitation.unmetDescription')} />
      </Screen>
    );
  }
  if (accept.isSuccess) {
    return (
      <Screen
        title={title}
        testID="trusted-invitation"
        footer={<Button title={t('invitation.done')} onPress={goBack} testID="trusted-invitation-done" />}
      >
        <Illustration name="done" size="sm" />
        <Text>{t('invitation.accepted')}</Text>
      </Screen>
    );
  }
  if (!invitation.data) {
    return (
      <Screen title={title} testID="trusted-invitation">
        {invitation.isError ? (
          <ErrorState description={t('invitation.loadError')} onRetry={() => void invitation.refetch()} />
        ) : (
          <LoadingState />
        )}
      </Screen>
    );
  }

  const name = invitation.data.ownerDisplayName;
  const confirmed = invitation.data.status === 'confirmed';
  const sheet = (
    <ConfirmSheet
      visible={confirming}
      title={t(confirmed ? 'invitation.leaveTitle' : 'invitation.declineTitle')}
      message={t(confirmed ? 'invitation.leaveMessage' : 'invitation.declineMessage', { name })}
      confirmLabel={t(confirmed ? 'invitation.leaveConfirm' : 'invitation.declineConfirm')}
      onConfirm={() => decline.mutate()}
      onClose={() => setConfirming(false)}
      loading={decline.isPending}
      destructive
      testID="trusted-invitation-decline-sheet"
    />
  );
  const declineFailed = decline.isError ? <Banner tone="danger">{t('invitation.declineFailed')}</Banner> : null;

  if (confirmed) {
    return (
      <Screen
        title={title}
        testID="trusted-invitation"
        footer={
          <>
            <Button title={t('invitation.done')} onPress={goBack} disabled={busy} testID="trusted-invitation-done" />
            <Button
              title={t('invitation.leave')}
              variant="secondary"
              disabled={busy}
              onPress={() => setConfirming(true)}
              testID="trusted-invitation-leave"
            />
          </>
        }
      >
        <Text>{t('invitation.confirmedIntro', { name })}</Text>
        <Text>{t('invitation.meaning')}</Text>
        {declineFailed}
        {sheet}
      </Screen>
    );
  }

  return (
    <Screen
      title={title}
      testID="trusted-invitation"
      footer={
        <>
          <Button
            title={t('invitation.accept')}
            loading={accept.isPending}
            disabled={decline.isPending}
            onPress={() => accept.mutate()}
            testID="trusted-invitation-accept"
          />
          <Button
            title={t('invitation.later')}
            variant="secondary"
            disabled={busy}
            onPress={goBack}
            testID="trusted-invitation-later"
          />
          <Button
            title={t('invitation.decline')}
            variant="secondary"
            disabled={busy}
            onPress={() => setConfirming(true)}
            testID="trusted-invitation-decline"
          />
        </>
      }
    >
      <Text>{t('invitation.intro', { name })}</Text>
      <Text>{t('invitation.meaning')}</Text>
      <Text tone="muted">{t('invitation.laterHint')}</Text>
      {accept.isError ? <Banner tone="danger">{t('invitation.failed')}</Banner> : null}
      {declineFailed}
      {sheet}
    </Screen>
  );
}
