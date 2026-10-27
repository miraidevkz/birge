import { SpecialistsScreen } from '@/screens/specialists';
import { SignedIn } from '@/screens/signed-in';

export default function SpecialistsRoute() {
  return (
    <SignedIn>
      <SpecialistsScreen />
    </SignedIn>
  );
}
