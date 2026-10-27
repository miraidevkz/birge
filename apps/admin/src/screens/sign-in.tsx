// Staff sign-in: OIDC authorization code + PKCE. The access token stays in
// memory only. On MFA step-up the API has already cleared the token; this
// screen shows the locale text and the same sign-in button.
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Screen, Text } from '@birge/ui';
import { useState } from 'react';

import { useSession } from '@/lib/session';

/** SignInScreenProps: onSignedIn runs after the token is in memory. */
export interface SignInScreenProps {
  onSignedIn: () => void;
}

/** SignInScreen starts the staff OIDC flow. */
export function SignInScreen({ onSignedIn }: SignInScreenProps) {
  const { t } = useTranslation('admin');
  const session = useSession();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<'cancelled' | 'failed' | null>(null);

  async function signIn() {
    setBusy(true);
    setError(null);
    session.clearMfa();
    try {
      await session.signIn();
      onSignedIn();
    } catch (err) {
      const message = err instanceof Error ? err.message : '';
      setError(message.includes('cancelled') ? 'cancelled' : 'failed');
    } finally {
      setBusy(false);
    }
  }

  if (session.mfaRequired) {
    return (
      <Screen
        title={t('mfa.title')}
        layout="form"
        footer={<Button title={t('mfa.button')} onPress={signIn} loading={busy} testID="mfa-sign-in" />}
      >
        <Text>{t('mfa.body')}</Text>
        {error ? (
          <Banner tone="danger">{error === 'cancelled' ? t('signIn.cancelled') : t('signIn.failed')}</Banner>
        ) : null}
      </Screen>
    );
  }

  return (
    <Screen
      title={t('signIn.title')}
      layout="form"
      footer={<Button title={t('signIn.button')} onPress={signIn} loading={busy} testID="staff-sign-in" />}
    >
      <Text>{t('signIn.intro')}</Text>
      {error ? (
        <Banner tone="danger">{error === 'cancelled' ? t('signIn.cancelled') : t('signIn.failed')}</Banner>
      ) : null}
    </Screen>
  );
}
