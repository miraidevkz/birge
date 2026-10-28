// An invitation to be another member's trusted contact (contract:
// acceptTrustedInvitation, `POST /me/trusted-invitations/{contactId}/accept`).
// Opened from the `emergency.trusted_invitation` inbox item, whose only param
// is the contact id.
//
// The contract has no operation to read an invitation, so the screen cannot
// name who invited her; it says what accepting means instead. It has no
// decline operation either: "Not now" only leaves the screen and the
// invitation stays open until she accepts or the inviter removes it.
//
// The server re-checks that the two have met in person (403) and hides a
// removed invitation or a block behind 404; both are final for this screen.
import { ApiError, unwrap } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, ErrorState, Illustration, Screen, Text } from '@birge/ui';
import { useMutation } from '@tanstack/react-query';

import { api } from '@/lib/api';
import { goBack } from '@/lib/nav';
import { isUuid } from '@/lib/notification-targets';

type Refusal = 'gone' | 'unmet' | null;

/** refusal tells a final answer from the server apart from one worth retrying. */
function refusal(err: unknown): Refusal {
  if (!(err instanceof ApiError)) return null;
  if (err.status === 404) return 'gone';
  if (err.status === 403) return 'unmet';
  return null;
}

/** TrustedInvitationScreen lets her accept being another member's trusted contact. */
export function TrustedInvitationScreen({ contactId }: { contactId: string }) {
  const { t } = useTranslation('emergency');
  const valid = isUuid(contactId);
  const accept = useMutation({
    mutationFn: () =>
      unwrap(api.POST('/me/trusted-invitations/{contactId}/accept', { params: { path: { contactId } } })),
  });

  const title = t('invitation.title');
  const final = valid ? refusal(accept.error) : 'gone';

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

  return (
    <Screen
      title={title}
      testID="trusted-invitation"
      footer={
        <>
          <Button
            title={t('invitation.accept')}
            loading={accept.isPending}
            onPress={() => accept.mutate()}
            testID="trusted-invitation-accept"
          />
          <Button
            title={t('invitation.later')}
            variant="secondary"
            disabled={accept.isPending}
            onPress={goBack}
            testID="trusted-invitation-later"
          />
        </>
      }
    >
      <Text>{t('invitation.intro')}</Text>
      <Text>{t('invitation.meaning')}</Text>
      <Text tone="muted">{t('invitation.laterHint')}</Text>
      {accept.isError ? <Banner tone="danger">{t('invitation.failed')}</Banner> : null}
    </Screen>
  );
}
