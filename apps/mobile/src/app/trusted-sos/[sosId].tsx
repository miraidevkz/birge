// An SOS of a member whose confirmed trusted contact she is (inbox
// `emergency.sos_raised_contact`). The id is checked as a UUID by the screen.
import { useLocalSearchParams } from 'expo-router';

import { SignedIn } from '@/screens/signed-in';
import { TrustedSosScreen } from '@/screens/trusted-sos';

export default function TrustedSosRoute() {
  const { sosId } = useLocalSearchParams<{ sosId: string }>();
  return (
    <SignedIn>
      <TrustedSosScreen sosId={typeof sosId === 'string' ? sosId : ''} />
    </SignedIn>
  );
}
