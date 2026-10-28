// Rooms: group chat rooms (district and topic) with their member counts and
// mentors (staffListRooms), and mentor assignment. "Add mentor" turns the
// screen into a picker over verified members (the Members list, one query
// shared with that screen); a tap assigns. Removal is confirmed. Mentor and
// member names stay on screen only: never logged, never in a query key.
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ConfirmSheet, List, ListItem, Screen, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Fragment, useState } from 'react';

import { api } from '@/lib/api';
import { useDistricts, useStaffMembers, type StaffMember } from '@/lib/members';
import { isNotImplemented } from '@/lib/not-implemented';
import { roomTitle } from '@/lib/room-title';

type StaffRoom = Schemas['StaffRoom'];
type Mentor = StaffRoom['mentors'][number];

const roomsKey = ['staff', 'rooms'] as const;

type Notice = 'assigned' | 'removed' | 'conflict' | 'failed';

interface Removal {
  room: StaffRoom;
  mentor: Mentor;
}

/** RoomsScreen lists group rooms and lets a moderator assign or remove their mentors. */
export function RoomsScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [picking, setPicking] = useState<StaffRoom | null>(null);
  const [removing, setRemoving] = useState<Removal | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);

  const rooms = useQuery({
    queryKey: roomsKey,
    queryFn: () => unwrap(api.GET('/staff/chat/rooms')),
  });
  // The picker's list: read (and audited) only while the picker is open.
  const verified = useStaffMembers('verified', undefined, picking !== null);
  const districts = useDistricts();

  const assign = useMutation({
    mutationFn: ({ roomId, memberId }: { roomId: string; memberId: string }) =>
      unwrap(api.PUT('/staff/chat/rooms/{roomId}/mentors/{memberId}', { params: { path: { roomId, memberId } } })),
    onSuccess: async () => {
      setPicking(null);
      setNotice('assigned');
      await client.invalidateQueries({ queryKey: roomsKey });
    },
    // 409: she does not hold the mentor role (the server checks it, not the client).
    onError: (err) => setNotice(err instanceof ApiError && err.status === 409 ? 'conflict' : 'failed'),
  });

  const remove = useMutation({
    mutationFn: ({ room, mentor }: Removal) =>
      unwrap(
        api.DELETE('/staff/chat/rooms/{roomId}/mentors/{memberId}', {
          params: { path: { roomId: room.id, memberId: mentor.memberId } },
        }),
      ),
    onSuccess: async () => {
      setRemoving(null);
      setNotice('removed');
      await client.invalidateQueries({ queryKey: roomsKey });
    },
    onError: () => {
      setRemoving(null);
      setNotice('failed');
    },
  });

  const noticeBanner = () => {
    switch (notice) {
      case 'assigned':
        return <Banner tone="success">{t('rooms.assigned')}</Banner>;
      case 'removed':
        return <Banner tone="success">{t('rooms.removed')}</Banner>;
      case 'conflict':
        return <Banner tone="warning">{t('rooms.conflict')}</Banner>;
      case 'failed':
        return <Banner tone="danger">{t('actions.failed')}</Banner>;
      default:
        return null;
    }
  };

  const notAvailable = { title: t('notAvailable.title'), description: t('notAvailable.description') };

  if (picking) {
    // A 501 is not an error: the list simply does not exist on this server yet.
    const pickerUnavailable = isNotImplemented(verified.error);
    const mentorIds = new Set(picking.mentors.map((m) => m.memberId));
    const pickerEmpty = pickerUnavailable
      ? notAvailable
      : { title: t('members.emptyVerifiedTitle'), description: t('members.emptyVerifiedDescription') };
    const choose = (memberId: string) => assign.mutate({ roomId: picking.id, memberId });
    return (
      <Screen title={t('rooms.pick.title')} scroll={false}>
        <List<StaffMember>
          data={pickerUnavailable ? [] : verified.data?.pages.flatMap((p) => p.items)}
          loading={verified.isLoading}
          error={pickerUnavailable ? undefined : verified.error}
          onRetry={() => void verified.refetch()}
          keyExtractor={(m) => m.id}
          empty={pickerEmpty}
          testID="mentor-picker"
          onLoadMore={verified.hasNextPage ? () => void verified.fetchNextPage() : undefined}
          loadingMore={verified.isFetchingNextPage}
          header={
            <>
              <Text>{t('rooms.pick.intro', { room: roomTitle(picking.title) })}</Text>
              <Button
                title={t('rooms.pick.back')}
                variant="secondary"
                onPress={() => {
                  setPicking(null);
                  setNotice(null);
                }}
                testID="picker-back"
              />
              {noticeBanner()}
            </>
          }
          renderItem={({ item }) => {
            const already = mentorIds.has(item.id);
            return (
              <ListItem
                title={item.displayName}
                subtitle={already ? t('rooms.pick.already') : districts.nameOf(item.districtId)}
                onPress={already || assign.isPending ? undefined : () => choose(item.id)}
                testID={`pick-${item.id}`}
              />
            );
          }}
        />
      </Screen>
    );
  }

  const roomsUnavailable = isNotImplemented(rooms.error);
  const roomsEmpty = roomsUnavailable
    ? notAvailable
    : { title: t('rooms.emptyTitle'), description: t('rooms.emptyDescription') };
  const removeMessage = removing
    ? t('rooms.removeMessage', { name: removing.mentor.displayName, room: roomTitle(removing.room.title) })
    : '';
  return (
    <Screen title={t('rooms.title')} scroll={false}>
      <List<StaffRoom>
        data={roomsUnavailable ? [] : rooms.data?.items}
        loading={rooms.isLoading}
        error={roomsUnavailable ? undefined : rooms.error}
        onRetry={() => void rooms.refetch()}
        keyExtractor={(r) => r.id}
        empty={roomsEmpty}
        testID="rooms"
        header={noticeBanner() ?? undefined}
        renderItem={({ item }) => (
          <Card testID={`room-${item.id}`}>
            <Text variant="label">{roomTitle(item.title)}</Text>
            <Text tone="muted">{t(`rooms.kind.${item.kind}`)}</Text>
            <Text tone="muted">{t('rooms.memberCount', { count: item.memberCount })}</Text>
            <Text variant="label">{t('rooms.mentors')}</Text>
            {item.mentors.length === 0 ? <Text tone="muted">{t('rooms.noMentors')}</Text> : null}
            {item.mentors.map((mentor) => (
              <Fragment key={mentor.memberId}>
                <Text>{mentor.displayName}</Text>
                <Button
                  title={t('rooms.removeMentor')}
                  variant="secondary"
                  size="sm"
                  onPress={() => {
                    setNotice(null);
                    setRemoving({ room: item, mentor });
                  }}
                  testID={`remove-${item.id}-${mentor.memberId}`}
                />
              </Fragment>
            ))}
            <Button
              title={t('rooms.addMentor')}
              variant="secondary"
              onPress={() => {
                setNotice(null);
                setPicking(item);
              }}
              testID={`add-mentor-${item.id}`}
            />
          </Card>
        )}
      />
      <ConfirmSheet
        visible={removing !== null}
        title={t('rooms.removeTitle')}
        message={removeMessage}
        confirmLabel={t('rooms.removeConfirm')}
        destructive
        onConfirm={() => {
          if (removing) remove.mutate(removing);
        }}
        onClose={() => setRemoving(null)}
        loading={remove.isPending}
        testID="remove-confirm"
      />
    </Screen>
  );
}
