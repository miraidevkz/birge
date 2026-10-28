// Moderation queue: oldest first, filtered by kind, paged by cursor.
// Decisions follow the contract (approve / reject with a reason; blockAuthor
// and legalHold only for reports and held chat messages). Summary text is
// content under review, not contact data; still never logged. Reason codes
// from the API are shown only through locale labels: an unknown code gets a
// generic label, never the raw code.
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import {
  Banner,
  Button,
  Card,
  CheckItem,
  ChoiceGroup,
  ConfirmSheet,
  List,
  Photo,
  Screen,
  Sheet,
  Text,
} from '@birge/ui';
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';
import { formatDateTime } from '@/lib/time';

type ModerationItem = Schemas['ModerationItem'];
type Kind = Schemas['ModerationKind'];
type Reason = NonNullable<Schemas['ModerationDecision']['reason']>;
type KindFilter = Kind | 'all';

const KINDS: Kind[] = ['listing', 'specialist', 'review', 'report', 'chat_message', 'verification'];

const REASONS: Reason[] = [
  'rules_medicine',
  'rules_money',
  'person_in_photo',
  'unsafe',
  'spam',
  'inaccurate',
  'duplicate',
  'other',
];

// A verification request is about a person, not content: only these reasons fit.
const VERIFICATION_REASONS: Reason[] = ['inaccurate', 'duplicate', 'other'];

// Codes the server sends today (chat model); anything else gets the generic label.
const HOLD_REASONS = ['crisis', 'newcomer', 'link', 'phone'];
const REPORT_REASONS = [
  'harassment',
  'scam_or_fundraising',
  'miracle_cure',
  'unsafe_content',
  'child_safety',
  'spam',
  'other',
];

const queueKey = ['staff', 'moderation'] as const;

// Reports and held chat messages are about one chat message: their objectId
// is the message ID, which the legal-hold endpoints take.
function chatKind(kind: Kind): boolean {
  return kind === 'report' || kind === 'chat_message';
}

interface Decision {
  itemId: string;
  approve: boolean;
  reason?: Reason;
  blockAuthor?: boolean;
  legalHold?: boolean;
}

interface HoldAction {
  messageId: string;
  on: boolean;
}

type Notice = 'holdOn' | 'holdOff' | 'alreadyDecided' | 'failed';

/** ModerationScreen lets a moderator filter, approve or reject queue items and set a legal hold on chat messages. */
export function ModerationScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [kind, setKind] = useState<KindFilter>('all');
  const [rejecting, setRejecting] = useState<ModerationItem | null>(null);
  const [reason, setReason] = useState<Reason>('spam');
  const [blockAuthor, setBlockAuthor] = useState(false);
  const [legalHold, setLegalHold] = useState(false);
  const [confirmingBlock, setConfirmingBlock] = useState(false);
  const [holdAction, setHoldAction] = useState<HoldAction | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);

  const queue = useInfiniteQuery({
    queryKey: [...queueKey, kind],
    queryFn: ({ pageParam }) =>
      unwrap(
        api.GET('/staff/moderation/queue', {
          params: { query: { kind: kind === 'all' ? undefined : kind, cursor: pageParam } },
        }),
      ),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.nextCursor,
  });

  const closeReject = () => {
    setRejecting(null);
    setConfirmingBlock(false);
  };

  const decide = useMutation({
    mutationFn: (d: Decision) =>
      unwrap(
        api.POST('/staff/moderation/items/{itemId}/decision', {
          params: { path: { itemId: d.itemId } },
          body: d.approve
            ? { approve: true }
            : {
                approve: false,
                reason: d.reason,
                ...(d.blockAuthor ? { blockAuthor: true } : {}),
                ...(d.legalHold ? { legalHold: true } : {}),
              },
        }),
      ),
    onSuccess: async () => {
      setNotice(null);
      closeReject();
      await client.invalidateQueries({ queryKey: queueKey });
    },
    onError: async (err) => {
      closeReject();
      if (err instanceof ApiError && err.status === 409) {
        setNotice('alreadyDecided');
        await client.invalidateQueries({ queryKey: queueKey });
        return;
      }
      setNotice('failed');
    },
  });

  // The API does not say whether a message is on hold, so both directions
  // are offered as explicit, confirmed actions (both are idempotent).
  const hold = useMutation({
    mutationFn: async ({ messageId, on }: HoldAction) => {
      const path = { params: { path: { messageId } } };
      if (on) await unwrap(api.PUT('/staff/chat/messages/{messageId}/legal-hold', path));
      else await unwrap(api.DELETE('/staff/chat/messages/{messageId}/legal-hold', path));
    },
    onSuccess: (_data, action) => {
      setHoldAction(null);
      setNotice(action.on ? 'holdOn' : 'holdOff');
    },
    onError: () => {
      setHoldAction(null);
      setNotice('failed');
    },
  });

  const startReject = (item: ModerationItem) => {
    setRejecting(item);
    setReason(item.kind === 'verification' ? 'inaccurate' : 'spam');
    setBlockAuthor(false);
    setLegalHold(false);
    setConfirmingBlock(false);
  };

  const sendReject = () => {
    if (!rejecting) return;
    const chat = chatKind(rejecting.kind);
    decide.mutate({
      itemId: rejecting.id,
      approve: false,
      reason,
      blockAuthor: chat && blockAuthor,
      legalHold: chat && legalHold,
    });
  };

  const holdLabel = (code: string) => t(`moderation.holdReason.${HOLD_REASONS.includes(code) ? code : 'unknown'}`);
  const reportLabel = (code: string) =>
    t(`moderation.reportReason.${REPORT_REASONS.includes(code) ? code : 'unknown'}`);

  const noticeBanner = () => {
    switch (notice) {
      case 'holdOn':
        return <Banner tone="success">{t('moderation.legalHold.putDone')}</Banner>;
      case 'holdOff':
        return <Banner tone="success">{t('moderation.legalHold.releaseDone')}</Banner>;
      case 'alreadyDecided':
        return <Banner tone="warning">{t('moderation.alreadyDecided')}</Banner>;
      case 'failed':
        return <Banner tone="danger">{t('actions.failed')}</Banner>;
      default:
        return null;
    }
  };

  const reasons = rejecting?.kind === 'verification' ? VERIFICATION_REASONS : REASONS;
  const rejectingChat = rejecting !== null && chatKind(rejecting.kind);

  return (
    <Screen title={t('moderation.title')} scroll={false}>
      <List<ModerationItem>
        data={queue.data?.pages.flatMap((p) => p.items)}
        loading={queue.isLoading}
        error={queue.error}
        onRetry={() => void queue.refetch()}
        keyExtractor={(item) => item.id}
        empty={{ title: t('moderation.emptyTitle'), description: t('moderation.emptyDescription') }}
        testID="moderation"
        onLoadMore={queue.hasNextPage ? () => void queue.fetchNextPage() : undefined}
        loadingMore={queue.isFetchingNextPage}
        header={
          <>
            <ChoiceGroup<KindFilter>
              label={t('moderation.filter.label')}
              options={[
                { value: 'all', label: t('moderation.filter.all') },
                ...KINDS.map((k) => ({ value: k, label: t(`moderation.kind.${k}`) })),
              ]}
              value={kind}
              onChange={setKind}
              testID="moderation-kind"
            />
            {noticeBanner()}
          </>
        }
        renderItem={({ item }) => (
          <Card testID={`moderation-${item.id}`}>
            <Text variant="label">{t(`moderation.kind.${item.kind}`)}</Text>
            <Text tone="muted">{t('moderation.createdAt', { time: formatDateTime(item.createdAt) })}</Text>
            {item.kind === 'chat_message' && item.holdReason ? (
              <Text tone="muted">{t('moderation.heldFor', { reason: holdLabel(item.holdReason) })}</Text>
            ) : null}
            {item.kind === 'report' && item.reportReason ? (
              <Text tone="muted">{t('moderation.reportedFor', { reason: reportLabel(item.reportReason) })}</Text>
            ) : null}
            {item.kind === 'report' && item.reportCount !== undefined ? (
              <Text tone="muted">{t('moderation.reportCount', { count: item.reportCount })}</Text>
            ) : null}
            <Text>{item.summary}</Text>
            {item.photoUrl ? (
              <Photo uri={item.photoUrl} accessibilityLabel={t('moderation.photo')} testID={`photo-${item.id}`} />
            ) : null}
            {item.kind === 'verification' ? <Text tone="muted">{t('moderation.verificationHint')}</Text> : null}
            <Button
              title={item.kind === 'verification' ? t('moderation.approveVerification') : t('moderation.approve')}
              onPress={() => decide.mutate({ itemId: item.id, approve: true })}
              loading={decide.isPending && decide.variables?.itemId === item.id && decide.variables.approve}
              testID={`approve-${item.id}`}
            />
            <Button
              title={t('moderation.reject')}
              variant="secondary"
              onPress={() => startReject(item)}
              testID={`reject-${item.id}`}
            />
            {chatKind(item.kind) ? (
              <>
                <Text variant="caption" tone="muted">
                  {t('moderation.legalHold.stateUnknown')}
                </Text>
                <Button
                  title={t('moderation.legalHold.put')}
                  variant="secondary"
                  onPress={() => setHoldAction({ messageId: item.objectId, on: true })}
                  testID={`hold-on-${item.id}`}
                />
                <Button
                  title={t('moderation.legalHold.release')}
                  variant="secondary"
                  onPress={() => setHoldAction({ messageId: item.objectId, on: false })}
                  testID={`hold-off-${item.id}`}
                />
              </>
            ) : null}
          </Card>
        )}
      />
      <Sheet
        visible={rejecting !== null && !confirmingBlock}
        onClose={closeReject}
        title={t('moderation.rejectTitle')}
        testID="reject-sheet"
      >
        <ChoiceGroup
          label={t('moderation.reason.label')}
          options={reasons.map((r) => ({ value: r, label: t(`moderation.reason.${r}`) }))}
          value={reason}
          onChange={setReason}
          testID="reject-reason"
        />
        {rejectingChat ? (
          <>
            <CheckItem
              title={t('moderation.blockAuthor.label')}
              checked={blockAuthor}
              onChange={setBlockAuthor}
              testID="reject-block-author"
            />
            <Text variant="caption" tone="muted">
              {t('moderation.blockAuthor.hint')}
            </Text>
            <CheckItem
              title={t('moderation.legalHoldOption.label')}
              checked={legalHold}
              onChange={setLegalHold}
              testID="reject-legal-hold"
            />
            <Text variant="caption" tone="muted">
              {t('moderation.legalHoldOption.hint')}
            </Text>
          </>
        ) : null}
        <Button
          title={t('moderation.confirmReject')}
          variant="destructive"
          onPress={() => {
            // Suspending a person is confirmed once more before anything is sent.
            if (rejectingChat && blockAuthor) setConfirmingBlock(true);
            else sendReject();
          }}
          loading={decide.isPending}
          testID="confirm-reject"
        />
      </Sheet>
      <ConfirmSheet
        visible={rejecting !== null && confirmingBlock}
        title={t('moderation.blockConfirm.title')}
        message={t('moderation.blockConfirm.message')}
        confirmLabel={t('moderation.blockConfirm.confirm')}
        destructive
        onConfirm={sendReject}
        onClose={closeReject}
        loading={decide.isPending}
        testID="block-confirm"
      />
      <ConfirmSheet
        visible={holdAction !== null}
        title={holdAction?.on === false ? t('moderation.legalHold.releaseTitle') : t('moderation.legalHold.putTitle')}
        message={
          holdAction?.on === false ? t('moderation.legalHold.releaseMessage') : t('moderation.legalHold.putMessage')
        }
        confirmLabel={holdAction?.on === false ? t('moderation.legalHold.release') : t('moderation.legalHold.put')}
        onConfirm={() => {
          if (holdAction) hold.mutate(holdAction);
        }}
        onClose={() => setHoldAction(null)}
        loading={hold.isPending}
        testID="hold-confirm"
      />
    </Screen>
  );
}
