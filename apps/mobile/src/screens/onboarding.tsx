// Onboarding: three short screens before the first sign-in (ADR-0025):
// welcome and language, what is inside, how her data is kept. Shown once per
// device; "Skip" goes straight to sign-in. Nothing here needs the network.
import { useTranslation } from '@birge/i18n';
import { Button, Card, Heading, Illustration, LanguagePicker, Logo, Screen, Text } from '@birge/ui';
import { useState } from 'react';

import { chooseLanguage, currentLanguage } from '@/lib/language';
import { storage } from '@/lib/storage';

/** ONBOARDED_KEY marks that this device has seen onboarding. */
export const ONBOARDED_KEY = 'birge.onboarded';

/** hasOnboarded reports whether onboarding was finished or skipped on this device. */
export async function hasOnboarded(): Promise<boolean> {
  try {
    return (await storage.get(ONBOARDED_KEY)) === '1';
  } catch {
    return false;
  }
}

const STEPS = ['welcome', 'inside', 'privacy'] as const;

/** OnboardingScreenProps: what to do when she finishes or skips. */
export interface OnboardingScreenProps {
  onDone: () => void;
}

/** OnboardingScreen walks through the three steps with Next, Back and Skip. */
export function OnboardingScreen({ onDone }: OnboardingScreenProps) {
  const { t } = useTranslation('onboarding');
  const [step, setStep] = useState(0);
  const key = STEPS[step] ?? 'welcome';
  const last = step === STEPS.length - 1;

  async function finish() {
    try {
      await storage.set(ONBOARDED_KEY, '1');
    } catch {
      // Not remembered: she sees onboarding again next time, nothing worse.
    }
    onDone();
  }

  return (
    <Screen
      layout="form"
      transitionKey={key}
      footer={
        <>
          <Button
            title={last ? t('start') : t('next')}
            onPress={() => (last ? void finish() : setStep(step + 1))}
            testID="onboarding-next"
          />
          {step > 0 ? (
            <Button title={t('back')} variant="secondary" onPress={() => setStep(step - 1)} testID="onboarding-back" />
          ) : (
            <Button title={t('skip')} variant="secondary" onPress={() => void finish()} testID="onboarding-skip" />
          )}
        </>
      }
      testID={`onboarding-${key}`}
    >
      {/* Step 1 keeps the mark with the product name; every step has its scene. */}
      {step === 0 ? <Logo size="lg" name={t('common:appName')} /> : null}
      <Illustration name={key} />
      <Heading>{t(`${key}.title`)}</Heading>
      <Text>{t(`${key}.body`)}</Text>
      {key === 'welcome' ? (
        <LanguagePicker
          value={currentLanguage()}
          onChange={(l) => void chooseLanguage(l)}
          testID="onboarding-language"
        />
      ) : null}
      {key === 'inside' ? (
        <>
          {(['checkin', 'circle', 'help'] as const).map((item) => (
            <Card key={item} tone={item === 'help' ? 'default' : 'accent'}>
              <Text variant="label">{t(`inside.${item}.title`)}</Text>
              <Text tone="muted">{t(`inside.${item}.body`)}</Text>
            </Card>
          ))}
        </>
      ) : null}
      {key === 'privacy' ? (
        <>
          {(['jurisdiction', 'ai', 'delete'] as const).map((item) => (
            <Card key={item}>
              <Text variant="label">{t(`privacy.${item}.title`)}</Text>
              <Text tone="muted">{t(`privacy.${item}.body`)}</Text>
            </Card>
          ))}
        </>
      ) : null}
      <Text tone="muted" accessibilityLiveRegion="polite">
        {t('progress', { step: step + 1, total: STEPS.length })}
      </Text>
    </Screen>
  );
}
