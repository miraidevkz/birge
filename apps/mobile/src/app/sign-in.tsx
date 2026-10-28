import { unwrap } from '@birge/api';
import { Redirect, router } from 'expo-router';
import { useEffect, useState } from 'react';

import { api } from '@/lib/api';
import { hasOnboarded } from '@/screens/onboarding';
import { needsSetup } from '@/screens/setup';
import { SignInScreen } from '@/screens/sign-in';

export default function SignInRoute() {
  // First visit on this device: onboarding comes before sign-in.
  const [onboarded, setOnboarded] = useState<boolean | null>(null);
  useEffect(() => {
    void hasOnboarded().then(setOnboarded);
  }, []);
  if (onboarded === null) return null;
  if (!onboarded) return <Redirect href="/onboarding" />;
  // After the first sign-in she gets the short setup (name, district and
  // child, verification); later sign-ins go straight home.
  async function afterSignIn() {
    const me = await unwrap(api.GET('/me')).catch(() => undefined);
    router.replace((await needsSetup(me?.displayName)) ? '/setup' : '/');
  }
  return <SignInScreen onSignedIn={() => void afterSignIn()} />;
}
