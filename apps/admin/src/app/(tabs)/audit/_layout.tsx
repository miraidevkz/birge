// Nested stack under the Audit tab (admin only): audit log, staff list and
// staff card. Headers stay off; each screen owns its Screen title, and the
// screens above the log get the shared Back from StackBack.
import { Stack } from 'expo-router';

export default function AuditLayout() {
  return <Stack screenOptions={{ headerShown: false, animation: 'none' }} />;
}
