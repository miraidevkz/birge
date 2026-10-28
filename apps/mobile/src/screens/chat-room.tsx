// One conversation: history, send (clientId + Idempotency-Key), report.
// When the crisis lexicon holds a message, show Help — never wait on the LLM.
import { unwrap, type Schemas } from '@birge/api';
import { i18n, useTranslation } from '@birge/i18n';
import { Banner, Button, List, MessageBubble, Screen, Sheet, TextField } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useState } from 'react';

import { api } from '@/lib/api';
import { roomTitle } from '@/lib/reference';
import { saveCrisisPhones } from '@/lib/crisis-phones';
import { newIdempotencyKey } from '@/lib/platform-crypto';

type ChatMessage = Schemas['ChatMessage'];
type ReportReason = Schemas['ReportInput']['reason'];

const REPORT_REASONS: ReportReason[] = [
  'harassment',
  'scam_or_fundraising',
  'miracle_cure',
  'unsafe_content',
  'child_safety',
  'spam',
  'other',
];

/** ChatRoomScreen shows messages in one room and lets the member send or report. */
export function ChatRoomScreen({ roomId, title }: { roomId: string; title?: string }) {
  const { t } = useTranslation('chat');
  const client = useQueryClient();
  const [text, setText] = useState('');
  const [held, setHeld] = useState(false);
  const [reportId, setReportId] = useState<string | null>(null);
  const [reportDone, setReportDone] = useState(false);

  const messages = useQuery({
    queryKey: ['chat-messages', roomId],
    queryFn: () =>
      unwrap(
        api.GET('/chat/rooms/{roomId}/messages', {
          params: { path: { roomId }, query: { limit: 50 } },
        }),
      ),
  });

  const send = useMutation({
    mutationFn: async (body: string) => {
      // clientId makes the body idempotent; the API client also sets Idempotency-Key.
      const clientId = newIdempotencyKey();
      return unwrap(
        api.POST('/chat/rooms/{roomId}/messages', {
          params: { path: { roomId } },
          body: { clientId, text: body },
        }),
      );
    },
    onSuccess: async (msg) => {
      setText('');
      if (msg.status === 'held') {
        setHeld(true);
        if (msg.crisisPhones) await saveCrisisPhones(msg.crisisPhones);
      }
      await client.invalidateQueries({ queryKey: ['chat-messages', roomId] });
    },
  });

  const report = useMutation({
    mutationFn: ({ messageId, reason }: { messageId: string; reason: ReportReason }) =>
      unwrap(
        api.POST('/chat/messages/{messageId}/report', {
          params: { path: { messageId } },
          body: { reason },
        }),
      ),
    onSuccess: () => {
      setReportId(null);
      setReportDone(true);
    },
  });

  const canSend = text.trim().length > 0 && !send.isPending;

  return (
    <Screen
      title={title ? roomTitle(title) : t('title')}
      scroll={false}
      footer={
        <>
          {held ? (
            <Banner
              tone="danger"
              action={
                <Button title={t('room.crisisAction')} variant="destructive" onPress={() => router.push('/sos')} />
              }
            >
              {t('room.held')}
            </Banner>
          ) : null}
          {reportDone ? <Banner tone="success">{t('room.reportDone')}</Banner> : null}
          <TextField
            label={t('room.composer')}
            value={text}
            onChangeText={setText}
            editable={!send.isPending}
            testID="chat-composer"
          />
          <Button
            title={t('room.send')}
            loading={send.isPending}
            disabled={!canSend}
            onPress={() => send.mutate(text.trim())}
            testID="chat-send"
          />
        </>
      }
    >
      <List<ChatMessage>
        data={messages.data}
        loading={messages.isLoading}
        error={messages.error}
        onRetry={() => void messages.refetch()}
        keyExtractor={(m) => m.id}
        renderItem={({ item }) => {
          const body = item.text ?? '';
          const title = body.length > 0 ? body : t('room.hidden');
          const canReport = !item.mine && item.status === 'visible' && body.length > 0;
          const time = new Intl.DateTimeFormat(i18n.language, { hour: '2-digit', minute: '2-digit' }).format(
            new Date(item.createdAt),
          );
          return (
            <MessageBubble
              text={title}
              author={item.authorName}
              mine={item.mine === true}
              time={time}
              muted={body.length === 0}
              onPress={canReport ? () => setReportId(item.id) : undefined}
              accessibilityLabel={canReport ? `${item.authorName}: ${body}. ${t('room.report')}` : undefined}
              testID={`message-${item.id}`}
            />
          );
        }}
        empty={{ title: t('room.emptyTitle'), description: t('room.emptyDescription') }}
        testID="chat-messages"
      />
      <Sheet
        visible={reportId !== null}
        onClose={() => setReportId(null)}
        title={t('room.reportTitle')}
        testID="report-sheet"
      >
        {REPORT_REASONS.map((reason) => (
          <Button
            key={reason}
            title={t(`room.reasons.${reason}`)}
            variant="secondary"
            loading={report.isPending && report.variables?.reason === reason}
            disabled={report.isPending}
            onPress={() => {
              if (!reportId) return;
              setReportDone(false);
              report.mutate({ messageId: reportId, reason });
            }}
            testID={`report-${reason}`}
          />
        ))}
      </Sheet>
    </Screen>
  );
}
