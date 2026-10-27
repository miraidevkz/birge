import { useTranslation } from '@birge/i18n';
import { Button } from '@birge/ui';
import { router } from 'expo-router';

import { useSession } from '@/lib/session';
import { PlaceholderScreen } from '@/screens/placeholder';

export default function ProfileRoute() {
  const { t } = useTranslation('profile');
  const session = useSession();
  return (
    <PlaceholderScreen ns="profile">
      <Button
        title={t('signOut')}
        variant="secondary"
        onPress={() => void session.signOut().then(() => router.replace('/sign-in'))}
      />
    </PlaceholderScreen>
  );
}
