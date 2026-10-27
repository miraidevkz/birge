// Staff profile: subject and roles from userinfo (if present), and sign-out.
// The access token is never displayed.
import { useTranslation } from '@birge/i18n';
import { Button, Card, Screen, Text } from '@birge/ui';
import { router } from 'expo-router';

import { useSession } from '@/lib/session';

/** ProfileScreen shows who is signed in and signs them out. */
export function ProfileScreen() {
  const { t } = useTranslation('admin');
  const session = useSession();
  const profile = session.profile;

  async function signOut() {
    await session.signOut();
    router.replace('/sign-in');
  }

  return (
    <Screen title={t('profile.title')}>
      <Card testID="staff-profile">
        <Text variant="label">{t('profile.subject')}</Text>
        <Text testID="staff-subject">{profile?.subject || '—'}</Text>
        <Text variant="label">{t('profile.roles')}</Text>
        <Text testID="staff-roles">
          {profile && profile.roles.length > 0 ? profile.roles.join(', ') : t('profile.noRoles')}
        </Text>
      </Card>
      <Button title={t('profile.signOut')} variant="secondary" onPress={signOut} testID="staff-sign-out" />
    </Screen>
  );
}
