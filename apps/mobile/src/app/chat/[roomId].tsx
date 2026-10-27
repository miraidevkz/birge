import { useLocalSearchParams } from 'expo-router';

import { ChatRoomScreen } from '@/screens/chat-room';
import { SignedIn } from '@/screens/signed-in';

export default function ChatRoomRoute() {
  const { roomId, title } = useLocalSearchParams<{ roomId: string; title?: string }>();
  return (
    <SignedIn>
      <ChatRoomScreen roomId={roomId} title={typeof title === 'string' ? title : undefined} />
    </SignedIn>
  );
}
