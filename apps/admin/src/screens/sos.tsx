// SOS queue for moderators and admins (requirements § 6: "Seen" and "Close"
// for SOS of the practical types; "I feel very bad" is an escalation, ADR-0023).
// Every SOS already went to the moderators and to her confirmed trusted
// contacts when it was raised; this queue is the moderators' second channel.
//
// - Urgent, so the list refetches when the tab gains focus and polls every
//   30 s while the open segment is on screen. No sound and no animation
//   (`Screen variant="crisis"`, ADR-0014).
// - "Seen" and "Close" answer with the SOS as now stored; the row in the
//   cache is replaced with it, so the first "Seen" (kept by the server) and
//   the close show at once without reloading the page.
// - 403 explains that only moderators and admins see SOS; 404/501 show the
//   calm not-available state; other errors offer a retry.
// - The text and her first name are content under care: they live only in
//   the query data, never in a query key or a log. The type and district come
//   through locale labels; an unknown type gets a generic label, never the
//   raw code.
// - Every listing is audited by the server per SOS shown, so polling stops
//   when the tab loses focus or the closed segment is chosen.
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ChoiceGroup, ConfirmSheet, List, Screen, Text } from '@birge/ui';
import { useInfiniteQuery, useMutation, useQueryClient, type InfiniteData } from '@tanstack/react-query';
import { useFocusEffect } from 'expo-router';
import { useCallback, useState } from 'react';

import { api } from '@/lib/api';
import { useDistricts } from '@/lib/members';
import { isForbidden, isNotImplemented } from '@/lib/not-implemented';
import { useSession } from '@/lib/session';
import { isAdmin, staffName, useStaffAccounts } from '@/lib/staff';
import { formatDateTime } from '@/lib/time';

type StaffSos = Schemas['StaffSos'];
type StaffSosPage = Schemas['StaffSosPage'];
type Status = StaffSos['status'];

const STATUSES: Status[] = ['open', 'closed'];
const TYPES: readonly string[] = ['childcare', 'item_or_medicine', 'other'];

/** POLL_MS is how often the open segment refreshes while it is on screen. */
const POLL_MS = 30_000;

const sosKey = ['staff', 'sos'] as const;

type Notice = 'forbidden' | 'gone' | 'notAvailable' | 'failed';

function noticeOf(err: unknown): Notice {
  if (isForbidden(err)) return 'forbidden';
  if (isNotImplemented(err)) return 'notAvailable';
  if (err instanceof ApiError && err.status === 404) return 'gone';
  return 'failed';
}

/** SosQueueScreen lists open and closed SOS requests and lets a moderator mark one seen or close it. */
export function SosQueueScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const { profile } = useSession();
  const [focused, setFocused] = useState(true);
  const [status, setStatus] = useState<Status>('open');
  const [closing, setClosing] = useState<string | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const districts = useDistricts();
  // Staff names come from the staff directory, which only admins may read;
  // a moderator sees the short staff ID instead.
  const accounts = useStaffAccounts(isAdmin(profile?.roles));

  useFocusEffect(
    useCallback(() => {
      setFocused(true);
      // cancelRefetch false: on the first focus the initial fetch is still
      // running and is reused instead of being started again.
      void client.refetchQueries({ queryKey: sosKey, type: 'active' }, { cancelRefetch: false });
      return () => setFocused(false);
    }, [client]),
  );

  const queue = useInfiniteQuery({
    queryKey: [...sosKey, status],
    queryFn: ({ pageParam }) => unwrap(api.GET('/staff/sos', { params: { query: { status, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor,
    refetchInterval: focused && status === 'open' ? POLL_MS : false,
  });

  // The server answers with the SOS as now stored: that row is replaced in
  // every cached page, so the screen shows it without another listing.
  const replaceRow = (row: StaffSos) => {
    client.setQueriesData<InfiniteData<StaffSosPage>>({ queryKey: sosKey }, (old) =>
      old
        ? {
            ...old,
            pages: old.pages.map((p) => ({ ...p, items: p.items.map((i) => (i.id === row.id ? row : i)) })),
          }
        : old,
    );
  };

  const failed = (err: unknown) => {
    const n = noticeOf(err);
    setNotice(n);
    // An SOS that is gone (account deleted) leaves the list on the next read.
    if (n === 'gone') void client.invalidateQueries({ queryKey: sosKey });
  };

  const seen = useMutation({
    mutationFn: (sosId: string) => unwrap(api.POST('/staff/sos/{sosId}/seen', { params: { path: { sosId } } })),
    onSuccess: (row) => {
      setNotice(null);
      replaceRow(row);
    },
    onError: failed,
  });

  const close = useMutation({
    mutationFn: (sosId: string) => unwrap(api.POST('/staff/sos/{sosId}/close', { params: { path: { sosId } } })),
    onSuccess: async (row) => {
      setNotice(null);
      setClosing(null);
      replaceRow(row);
      // The closed segment now has one more row; it reloads when chosen.
      await client.invalidateQueries({ queryKey: [...sosKey, 'closed'], refetchType: 'none' });
    },
    onError: (err) => {
      setClosing(null);
      failed(err);
    },
  });

  const forbidden = isForbidden(queue.error);
  const notAvailable = isNotImplemented(queue.error);
  const calm = forbidden || notAvailable;

  const typeLabel = (type: string) => t(`sos.type.${TYPES.includes(type) ? type : 'unknown'}`);

  const districtLine = (id: string | undefined) => {
    if (!id) return t('sos.noDistrict');
    const name = districts.nameOf(id);
    return name ? t('sos.district', { name }) : t('sos.districtUnknown');
  };

  const seenByName = (id: string) => staffName(id, accounts.data?.items.find((a) => a.id === id)?.displayName);

  const emptyState = () => {
    if (forbidden) return { title: t('sos.forbiddenTitle'), description: t('sos.forbiddenDescription') };
    if (notAvailable) return { title: t('notAvailable.title'), description: t('notAvailable.description') };
    return status === 'open'
      ? { title: t('sos.emptyOpenTitle'), description: t('sos.emptyOpenDescription') }
      : { title: t('sos.emptyClosedTitle'), description: t('sos.emptyClosedDescription') };
  };

  const noticeBanner = () => {
    switch (notice) {
      case 'forbidden':
        return <Banner tone="warning">{t('sos.forbiddenDescription')}</Banner>;
      case 'notAvailable':
        return <Banner tone="warning">{t('notAvailable.description')}</Banner>;
      case 'gone':
        return <Banner tone="warning">{t('sos.gone')}</Banner>;
      case 'failed':
        return <Banner tone="danger">{t('actions.failed')}</Banner>;
      default:
        return null;
    }
  };

  // A 403 or a route the server does not serve yet is not an error banner:
  // the queue does not exist for this person or on this server.
  const rows = calm ? [] : queue.data?.pages.flatMap((p) => p.items);

  return (
    <Screen title={t('sos.title')} scroll={false} variant="crisis">
      <List<StaffSos>
        data={rows}
        loading={queue.isLoading}
        error={calm ? undefined : queue.error}
        onRetry={() => void queue.refetch()}
        keyExtractor={(item) => item.id}
        empty={emptyState()}
        testID="sos"
        onLoadMore={queue.hasNextPage && !calm ? () => void queue.fetchNextPage() : undefined}
        loadingMore={queue.isFetchingNextPage}
        header={
          forbidden ? undefined : (
            <>
              <Text tone="muted">{t('sos.intro')}</Text>
              <ChoiceGroup
                label={t('sos.status.label')}
                options={STATUSES.map((s) => ({ value: s, label: t(`sos.status.${s}`) }))}
                value={status}
                onChange={(s) => {
                  setNotice(null);
                  setStatus(s);
                }}
                testID="sos-status"
              />
              {noticeBanner()}
            </>
          )
        }
        renderItem={({ item }) => (
          <Card testID={`sos-${item.id}`}>
            <Text variant="label">{typeLabel(item.type)}</Text>
            {item.type === 'childcare' ? (
              <Text tone="warning" testID={`sos-childcare-${item.id}`}>
                {t('sos.childcareOnly')}
              </Text>
            ) : null}
            <Text>{item.text}</Text>
            <Text tone="muted">{t('sos.author', { name: item.displayName })}</Text>
            <Text tone="muted">{districtLine(item.districtId)}</Text>
            <Text tone="muted">{t('sos.createdAt', { time: formatDateTime(item.createdAt) })}</Text>
            {item.status === 'open' && item.closesAt ? (
              <Text tone="muted">{t('sos.closesAt', { time: formatDateTime(item.closesAt) })}</Text>
            ) : null}
            {item.status === 'closed' ? (
              <Text tone="muted">
                {item.closedAt ? t('sos.closedAt', { time: formatDateTime(item.closedAt) }) : t('sos.closedNoTime')}
              </Text>
            ) : null}
            <Text tone="muted">{t('sos.responses', { count: item.responseCount })}</Text>
            {item.type === 'childcare' ? null : (
              <Text tone="muted">{item.sharedToDistrict ? t('sos.shared') : t('sos.notShared')}</Text>
            )}
            <Text tone="muted" testID={`sos-seen-${item.id}`}>
              {item.seenAt
                ? t('sos.seenBy', {
                    name: item.seenBy ? seenByName(item.seenBy) : t('sos.seenByUnknown'),
                    time: formatDateTime(item.seenAt),
                  })
                : t('sos.notSeen')}
            </Text>
            {item.status === 'open' && !item.seenAt ? (
              <Button
                title={t('sos.seen')}
                variant="secondary"
                onPress={() => seen.mutate(item.id)}
                loading={seen.isPending && seen.variables === item.id}
                testID={`seen-${item.id}`}
              />
            ) : null}
            {item.status === 'open' ? (
              <Button title={t('sos.close')} onPress={() => setClosing(item.id)} testID={`close-${item.id}`} />
            ) : null}
          </Card>
        )}
      />
      <ConfirmSheet
        visible={closing !== null}
        title={t('sos.closeConfirm.title')}
        message={t('sos.closeConfirm.message')}
        confirmLabel={t('sos.closeConfirm.confirm')}
        onConfirm={() => {
          if (closing) close.mutate(closing);
        }}
        onClose={() => setClosing(null)}
        loading={close.isPending}
        testID="sos-close-confirm"
      />
    </Screen>
  );
}
