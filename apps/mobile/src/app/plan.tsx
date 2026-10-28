// The plan page for an external trusted contact (SMS link `/plan#<token>`).
// Web only: the link is opened in a browser, never in the app; on a phone
// with the app installed the route just goes home. Deliberately outside the
// signed-in flow: no SignedIn gate, no onboarding redirect, no tabs, and a
// root of the stack (ROOT_ROUTES) so there is no Back into the app.
import { Redirect } from 'expo-router';
import { Platform } from 'react-native';

import { ExternalPlanScreen } from '@/screens/external-plan';

export default function PlanRoute() {
  if (Platform.OS !== 'web') return <Redirect href="/" />;
  return <ExternalPlanScreen />;
}
