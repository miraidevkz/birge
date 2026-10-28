// After a successful deletion request; the session is already gone, so there
// is no SignedIn gate. A root of the stack (ROOT_ROUTES): no Back.
import { AccountDeletedScreen } from '@/screens/account-deleted';

export default function AccountDeletedRoute() {
  return <AccountDeletedScreen />;
}
