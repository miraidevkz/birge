import { useLocalSearchParams } from 'expo-router';

import { KnowledgeArticleScreen } from '@/screens/knowledge-article';

export default function KnowledgeArticleRoute() {
  const { articleId } = useLocalSearchParams<{ articleId: string }>();
  return <KnowledgeArticleScreen articleId={typeof articleId === 'string' ? articleId : ''} />;
}
