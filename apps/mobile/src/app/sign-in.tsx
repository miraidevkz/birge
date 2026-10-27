import { router } from 'expo-router';

import { SignInScreen } from '@/screens/sign-in';

export default function SignInRoute() {
  return <SignInScreen onSignedIn={() => router.replace('/')} />;
}
