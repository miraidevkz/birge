// The last screen after a successful deletion request (contract:
// requestDeletion). By the time it shows, the session is already gone from
// this phone, so it needs no data and no network: it says what happens next
// (internal/domains/privacy/README.md: every context erases her rows and her
// keys are shredded within 24 hours, so nothing can be restored) and offers
// one way back to the start. A root of the stack (ROOT_ROUTES): Back would
// lead into screens that need the account she just deleted.
import { useTranslation } from '@birge/i18n';
import { Button, Heading, Illustration, Screen, Text } from '@birge/ui';
import { router } from 'expo-router';

/** AccountDeletedScreen confirms the deletion request and leads back to the start. */
export function AccountDeletedScreen() {
  const { t } = useTranslation('privacy');
  return (
    <Screen
      title={t('deleted.title')}
      testID="account-deleted"
      footer={<Button title={t('deleted.action')} onPress={() => router.replace('/')} testID="account-deleted-start" />}
    >
      <Illustration name="done" size="sm" />
      <Text>{t('deleted.lead')}</Text>
      <Heading>{t('deleted.nextHeading')}</Heading>
      <Text>{t('deleted.what')}</Text>
      <Text>{t('deleted.keys')}</Text>
      <Text tone="muted">{t('deleted.thanks')}</Text>
    </Screen>
  );
}
