import { Redirect, router } from 'expo-router';

import { useSession } from '@/lib/session';
import { SignInScreen } from '@/screens/sign-in';

export default function SignInRoute() {
  const { signedIn, mfaRequired } = useSession();
  if (signedIn && !mfaRequired) return <Redirect href="/" />;
  return <SignInScreen onSignedIn={() => router.replace('/')} />;
}
