import { router } from 'expo-router';

import { SetupScreen } from '@/screens/setup';

export default function SetupRoute() {
  return <SetupScreen onDone={() => router.replace('/')} />;
}
