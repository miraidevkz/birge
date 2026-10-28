// An invitation to be another member's trusted contact (inbox
// `emergency.trusted_invitation`). The id is checked as a UUID by the screen.
import { useLocalSearchParams } from 'expo-router';

import { SignedIn } from '@/screens/signed-in';
import { TrustedInvitationScreen } from '@/screens/trusted-invitation';

export default function TrustedInvitationRoute() {
  const { contactId } = useLocalSearchParams<{ contactId: string }>();
  return (
    <SignedIn>
      <TrustedInvitationScreen contactId={typeof contactId === 'string' ? contactId : ''} />
    </SignedIn>
  );
}
