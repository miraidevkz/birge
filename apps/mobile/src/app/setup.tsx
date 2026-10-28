import { router } from 'expo-router';

import { offerPush } from '@/lib/push';
import { SetupScreen } from '@/screens/setup';

// The end of the first-run setup is the calm moment to offer notifications
// (asked once per phone; iOS and Android only, see lib/push.ts).
function done() {
  router.replace('/');
  void offerPush();
}

export default function SetupRoute() {
  return <SetupScreen onDone={done} />;
}
