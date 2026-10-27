// Nested stack under the Knowledge tab: articles list, editor, review queue
// and review-due. Headers stay off; each screen owns its Screen title.
import { Stack } from 'expo-router';

export default function KnowledgeLayout() {
  return <Stack screenOptions={{ headerShown: false, animation: 'none' }} />;
}
