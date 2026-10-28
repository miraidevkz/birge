import { describe, expect, it, jest } from '@jest/globals';
import { i18n } from '@birge/i18n';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { LANGUAGE_KEY } from '@/lib/language';
import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { hasOnboarded, OnboardingScreen } from './onboarding';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());

async function renderOnboarding(onDone = jest.fn()) {
  await render(
    <Providers>
      <OnboardingScreen onDone={onDone} />
    </Providers>,
  );
  return onDone;
}

describe('OnboardingScreen', () => {
  it('walks through three steps and remembers that it was seen', async () => {
    const onDone = await renderOnboarding();
    expect(screen.getByTestId('onboarding-welcome')).toBeTruthy();
    expect(screen.getByText('Step 1 of 3')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('onboarding-next'));
    expect(screen.getByTestId('onboarding-inside')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('onboarding-back'));
    expect(screen.getByTestId('onboarding-welcome')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('onboarding-next'));
    await fireEvent.press(screen.getByTestId('onboarding-next'));
    expect(screen.getByTestId('onboarding-privacy')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Get started' }));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(await hasOnboarded()).toBe(true);
  });

  it('skip goes straight on and is remembered too', async () => {
    await storage.remove('birge.onboarded');
    const onDone = await renderOnboarding();
    await fireEvent.press(screen.getByTestId('onboarding-skip'));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(await hasOnboarded()).toBe(true);
  });

  it('the language chosen on the first step switches the app and is kept', async () => {
    await renderOnboarding();
    await fireEvent.press(screen.getByText('Русский'));
    await waitFor(() => expect(i18n.language).toBe('ru'));
    expect(await storage.get(LANGUAGE_KEY)).toBe('ru');
    await i18n.changeLanguage('en');
  });
});
