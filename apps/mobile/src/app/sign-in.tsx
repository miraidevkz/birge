import { Redirect, router } from 'expo-router';
import { useEffect, useState } from 'react';

import { hasOnboarded } from '@/screens/onboarding';
import { SignInScreen } from '@/screens/sign-in';

export default function SignInRoute() {
  // First visit on this device: onboarding comes before sign-in.
  const [onboarded, setOnboarded] = useState<boolean | null>(null);
  useEffect(() => {
    void hasOnboarded().then(setOnboarded);
  }, []);
  if (onboarded === null) return null;
  if (!onboarded) return <Redirect href="/onboarding" />;
  return <SignInScreen onSignedIn={() => router.replace('/')} />;
}
