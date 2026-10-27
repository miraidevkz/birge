import { ListingsScreen } from '@/screens/listings';
import { SignedIn } from '@/screens/signed-in';

export default function ListingsRoute() {
  return (
    <SignedIn>
      <ListingsScreen />
    </SignedIn>
  );
}
