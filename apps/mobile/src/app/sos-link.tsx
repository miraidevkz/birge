// The SOS page for an external trusted contact (SMS link `/sos-link#<token>`).
// Not `/sos`: that is the mother's own SOS screen, which must stay exactly
// what it is. Web only, like `/plan`: on a phone with the app installed the
// route just goes home. Outside the signed-in flow: no SignedIn gate, no
// onboarding redirect, no tabs, and a root of the stack (ROOT_ROUTES) so
// there is no Back into the app.
import { Redirect } from 'expo-router';
import { Platform } from 'react-native';

import { ExternalSosScreen } from '@/screens/external-sos';

export default function SosLinkRoute() {
  if (Platform.OS !== 'web') return <Redirect href="/" />;
  return <ExternalSosScreen />;
}
