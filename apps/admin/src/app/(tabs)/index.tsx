import { Redirect } from 'expo-router';

import { useSession } from '@/lib/session';
import { canSeeTab, firstTabHref } from '@/lib/staff';
import { EscalationsScreen } from '@/screens/escalations';

export default function EscalationsRoute() {
  const { profile } = useSession();
  // The first tab is the escalation queue; roles it does not serve (admin,
  // lawyer) land on their own first tab instead of a 403.
  if (!canSeeTab('escalations', profile?.roles)) return <Redirect href={firstTabHref(profile?.roles)} />;
  return <EscalationsScreen />;
}
