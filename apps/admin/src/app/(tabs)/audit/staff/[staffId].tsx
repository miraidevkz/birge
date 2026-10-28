import { StackBack } from '@/lib/stack-back';
import { StaffCardScreen } from '@/screens/staff-card';

export default function StaffCardRoute() {
  return (
    <StackBack fallback="/audit/staff">
      <StaffCardScreen />
    </StackBack>
  );
}
