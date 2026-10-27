// The help screen reached from the SOS entry on any screen, signed in or not.
// It is outside the tabs so the sign-in gate never stands in front of it.
import { HelpScreen } from '@/screens/help';

export default function SosRoute() {
  return <HelpScreen source="menu" />;
}
