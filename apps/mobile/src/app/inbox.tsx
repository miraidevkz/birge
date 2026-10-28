import { InboxScreen } from '@/screens/inbox';
import { SignedIn } from '@/screens/signed-in';

export default function InboxRoute() {
  return (
    <SignedIn>
      <InboxScreen />
    </SignedIn>
  );
}
