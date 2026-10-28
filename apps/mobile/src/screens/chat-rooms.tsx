// Conversation list: rooms the member has joined, plus group rooms she may join.
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Avatar, Button, Heading, List, ListItem, Screen, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';

import { api } from '@/lib/api';
import { roomTitle } from '@/lib/reference';

type Room = Schemas['Room'];

function roomSubtitle(room: Room, t: (key: string) => string): string {
  const parts = [t(`rooms.kind.${room.kind}`)];
  if (room.muted) parts.push(t('rooms.muted'));
  return parts.join(' · ');
}

/** ChatRoomsScreen lists joined conversations and discoverable group rooms. */
export function ChatRoomsScreen() {
  const { t } = useTranslation('chat');
  const client = useQueryClient();

  const joined = useQuery({
    queryKey: ['chat-rooms', { joined: true }],
    queryFn: () => unwrap(api.GET('/chat/rooms', { params: { query: { joined: true } } })),
  });
  const discover = useQuery({
    queryKey: ['chat-rooms', { joined: false }],
    queryFn: () => unwrap(api.GET('/chat/rooms', { params: { query: { joined: false } } })),
  });

  const join = useMutation({
    mutationFn: (roomId: string) => unwrap(api.POST('/chat/rooms/{roomId}/join', { params: { path: { roomId } } })),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['chat-rooms'] });
    },
  });

  const discoverable = discover.data?.filter((r) => !r.joined) ?? [];
  // Group rooms are for verified members only (chat service): an unverified
  // member is told why the list is empty and where to ask, not to "join".
  const me = useQuery({ queryKey: ['me'], queryFn: () => unwrap(api.GET('/me')) });
  const unverified = me.data !== undefined && me.data.verification !== 'verified';
  const empty = unverified
    ? {
        title: t('rooms.unverifiedTitle'),
        description: t('rooms.unverifiedBody'),
        action: { label: t('rooms.toProfile'), onPress: () => router.push('/profile') },
      }
    : { title: t('rooms.emptyTitle'), description: t('rooms.emptyDescription') };

  return (
    <Screen title={t('title')} scroll={false}>
      <List<Room>
        data={joined.data}
        loading={joined.isLoading}
        error={joined.error}
        onRetry={() => void joined.refetch()}
        keyExtractor={(r) => r.id}
        renderItem={({ item }) => (
          <ListItem
            leading={<Avatar name={roomTitle(item.title)} />}
            title={roomTitle(item.title)}
            subtitle={roomSubtitle(item, t)}
            onPress={() => router.push(`/chat/${item.id}`)}
            testID={`room-${item.id}`}
          />
        )}
        empty={empty}
        header={
          discoverable.length > 0 ? (
            <>
              <Heading>{t('rooms.discoverHeading')}</Heading>
              {discoverable.map((room) => (
                <ListItem
                  key={room.id}
                  title={roomTitle(room.title)}
                  subtitle={roomSubtitle(room, t)}
                  trailing={
                    <Button
                      title={t('rooms.join')}
                      variant="secondary"
                      size="sm"
                      loading={join.isPending && join.variables === room.id}
                      onPress={() => join.mutate(room.id)}
                      testID={`join-${room.id}`}
                    />
                  }
                  testID={`discover-${room.id}`}
                />
              ))}
              <Text variant="label">{t('title')}</Text>
            </>
          ) : undefined
        }
        testID="chat-rooms"
      />
    </Screen>
  );
}
