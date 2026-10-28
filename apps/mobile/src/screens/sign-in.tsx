// Sign-in with phone number and SMS code (ADR-0008). Two steps on one
// screen; the pending flow lives only in component state.
//
// Privacy: the number and the code are never logged or stored. Errors are
// shown from locale texts by code, never with the input echoed.
import { E164, SignInError, type PendingSignIn, type SignInErrorCode } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Heading, Logo, PhoneField, Screen, Text, TextField } from '@birge/ui';
import { useState } from 'react';

import { kratos } from '@/lib/api';
import { config } from '@/lib/config';
import { useDemoCode } from '@/lib/demo-code';
import { useSession } from '@/lib/session';

function codeOf(err: unknown): SignInErrorCode {
  return err instanceof SignInError ? err.code : 'unknown';
}

/** SignInScreenProps: onSignedIn runs after the session is stored (the route navigates). */
export interface SignInScreenProps {
  onSignedIn: () => void;
}

/** SignInScreen asks for the phone, then for the code. */
export function SignInScreen({ onSignedIn }: SignInScreenProps) {
  const { t } = useTranslation('auth');
  const session = useSession();
  const [phone, setPhone] = useState('');
  const [code, setCode] = useState('');
  const [pending, setPending] = useState<PendingSignIn | null>(null);
  const [error, setError] = useState<SignInErrorCode | null>(null);
  const [busy, setBusy] = useState(false);
  const [sentAt, setSentAt] = useState(0);
  // Demo builds only (lib/demo-code): the code of a synthetic demo number.
  const demoCode = useDemoCode(pending?.phone ?? null, sentAt);

  async function sendCode() {
    if (!E164.test(phone)) {
      setError('invalid_phone');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      setPending(await kratos.startSignIn(phone));
      setSentAt(Date.now());
      setCode('');
    } catch (err) {
      setError(codeOf(err));
    } finally {
      setBusy(false);
    }
  }

  async function verify() {
    if (!pending) return;
    setBusy(true);
    setError(null);
    try {
      const s = await kratos.verifyCode(pending, code);
      await session.signIn(s);
      onSignedIn();
    } catch (err) {
      const c = codeOf(err);
      // An expired flow cannot take another code: go back to the number.
      if (c === 'flow_expired') setPending(null);
      setError(c);
    } finally {
      setBusy(false);
    }
  }

  const errorText = error ? t(`errors.${error}`) : undefined;

  if (!pending) {
    return (
      <Screen layout="form" footer={<Button title={t('sendCode')} onPress={sendCode} loading={busy} />}>
        {/* The brand opens the app: the name is the screen's heading here. */}
        <Logo size="lg" name={t('common:appName')} />
        <Heading>{t('title')}</Heading>
        <Text>{t('intro')}</Text>
        {config.demoCodesUrl ? <Text tone="muted">{t('demo.numbers')}</Text> : null}
        <PhoneField
          label={t('phone.label')}
          hint={t('phone.hint')}
          error={errorText}
          value={phone}
          onChangePhone={setPhone}
          returnKeyType="send"
          onSubmitEditing={sendCode}
          testID="sign-in-phone"
        />
      </Screen>
    );
  }

  return (
    <Screen
      title={t('title')}
      layout="form"
      footer={
        <>
          <Button title={t('verify')} onPress={verify} loading={busy} />
          <Button
            title={t('changeNumber')}
            variant="secondary"
            disabled={busy}
            onPress={() => {
              setPending(null);
              setError(null);
            }}
          />
        </>
      }
    >
      {demoCode !== undefined ? (
        <Banner tone="info" testID="sign-in-demo-code">
          {demoCode ? t('demo.banner', { code: demoCode }) : t('demo.waiting')}
        </Banner>
      ) : null}
      <TextField
        label={t('code.label')}
        hint={t('code.hint', { phone: pending.phone })}
        error={errorText}
        value={code}
        onChangeText={setCode}
        keyboardType="number-pad"
        textContentType="oneTimeCode"
        autoComplete="sms-otp"
        maxLength={12}
        autoFocus
        returnKeyType="done"
        onSubmitEditing={verify}
        testID="sign-in-code"
      />
    </Screen>
  );
}
