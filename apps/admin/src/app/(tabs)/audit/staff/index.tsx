import { StackBack } from '@/lib/stack-back';
import { StaffListScreen } from '@/screens/staff-list';

export default function StaffListRoute() {
  return (
    <StackBack fallback="/audit">
      <StaffListScreen />
    </StackBack>
  );
}
