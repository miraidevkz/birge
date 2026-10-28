import { router } from 'expo-router';

import { OnboardingScreen } from '@/screens/onboarding';

export default function OnboardingRoute() {
  return <OnboardingScreen onDone={() => router.replace('/sign-in')} />;
}
