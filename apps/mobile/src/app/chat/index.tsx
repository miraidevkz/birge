import { ChatRoomsScreen } from '@/screens/chat-rooms';
import { SignedIn } from '@/screens/signed-in';

export default function ChatRoomsRoute() {
  return (
    <SignedIn>
      <ChatRoomsScreen />
    </SignedIn>
  );
}
