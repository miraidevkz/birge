// Sign-in with phone number and SMS code (ADR-0008). Two steps on one
// screen; the pending flow lives only in component state.
//
// Privacy: the number and the code are never logged or stored. Errors are
// shown from locale texts by code, never with the input echoed.
import { E164, SignInError, type PendingSignIn, type SignInErrorCode } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Button, Screen, Text, TextField } from '@birge/ui';
import { useState } from 'react';

import { kratos } from '@/lib/api';
import { useSession } from '@/lib/session';

/** normalizePhone removes spaces, dashes and brackets people type or paste. */
export function normalizePhone(input: string): string {
  return input.replace(/[\s\-().]/g, '');
}

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

  async function sendCode() {
    const normalized = normalizePhone(phone);
    if (!E164.test(normalized)) {
      setError('invalid_phone');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      setPending(await kratos.startSignIn(normalized));
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
      <Screen
        title={t('title')}
        layout="form"
        footer={<Button title={t('sendCode')} onPress={sendCode} loading={busy} />}
      >
        <Text>{t('intro')}</Text>
        <TextField
          label={t('phone.label')}
          hint={t('phone.hint')}
          error={errorText}
          value={phone}
          onChangeText={setPhone}
          keyboardType="phone-pad"
          textContentType="telephoneNumber"
          autoComplete="tel"
          maxLength={20}
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
