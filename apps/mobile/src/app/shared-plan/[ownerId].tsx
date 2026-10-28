// The plan another member sent to her as a trusted contact (inbox
// `emergency.plan_sent`). The id is checked as a UUID by the screen.
import { useLocalSearchParams } from 'expo-router';

import { SharedPlanScreen } from '@/screens/shared-plan';
import { SignedIn } from '@/screens/signed-in';

export default function SharedPlanRoute() {
  const { ownerId } = useLocalSearchParams<{ ownerId: string }>();
  return (
    <SignedIn>
      <SharedPlanScreen ownerId={typeof ownerId === 'string' ? ownerId : ''} />
    </SignedIn>
  );
}
