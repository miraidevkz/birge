// Circle: tea requests, match cards (accept/decline), and links to chats,
// listings and specialists. Never suggests leaving a child with anyone
// (AGENTS.md non-negotiable 5); contacts stay private until both accept.
import { ApiError, unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Avatar, Banner, Button, Card, Heading, List, ListItem, Screen, Sheet, Text } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { router } from 'expo-router';
import { useMemo, useState } from 'react';

import { api } from '@/lib/api';

type Match = Schemas['Match'];
type TeaFormat = Schemas['TeaRequestInput']['format'];

type TeaDraft = {
  format: TeaFormat;
  sameDistrict: boolean;
  similarAge: boolean;
  sharedNeeds: boolean;
};

const defaultDraft = (): TeaDraft => ({
  format: 'call',
  sameDistrict: true,
  similarAge: true,
  sharedNeeds: true,
});

function matchSubtitle(m: Match, t: (key: string, opts?: Record<string, string>) => string): string {
  const parts = [
    m.format === 'meeting' ? t('matches.formatMeeting') : t('matches.formatCall'),
    t(`matches.status.${m.status}`),
  ];
  if (m.otherChildAges && m.otherChildAges.length > 0) {
    parts.push(t('matches.ages', { ages: m.otherChildAges.join(', ') }));
  }
  if (m.sharedNeeds.length > 0) {
    parts.push(t('matches.sharedNeeds', { needs: m.sharedNeeds.join(', ') }));
  }
  return parts.join(' · ');
}

function teaErrorMessage(err: unknown, t: (key: string) => string): string {
  if (err instanceof ApiError && err.status === 409) return t('tea.errorConflict');
  if (err instanceof ApiError && err.status === 403) return t('tea.errorForbidden');
  return t('tea.errorGeneric');
}

/** CircleScreen is the peer-matching tab: tea, matches and entry points to chat and lists. */
export function CircleScreen() {
  const { t } = useTranslation('circle');
  const client = useQueryClient();
  const [sheetOpen, setSheetOpen] = useState(false);
  const [draft, setDraft] = useState<TeaDraft>(defaultDraft);
  const [teaError, setTeaError] = useState<string | null>(null);

  const tea = useQuery({
    queryKey: ['tea-requests'],
    queryFn: () => unwrap(api.GET('/me/tea-requests')),
  });
  const matches = useQuery({
    queryKey: ['matches'],
    queryFn: () => unwrap(api.GET('/me/matches')),
  });

  const openTea = useMemo(() => tea.data?.find((r) => r.status === 'open'), [tea.data]);

  const createTea = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST('/me/tea-requests', {
          body: {
            format: draft.format,
            sameDistrict: draft.sameDistrict,
            similarAge: draft.similarAge,
            sharedNeeds: draft.sharedNeeds,
          },
        }),
      ),
    onSuccess: async () => {
      setSheetOpen(false);
      setTeaError(null);
      setDraft(defaultDraft());
      await client.invalidateQueries({ queryKey: ['tea-requests'] });
    },
    onError: (err) => setTeaError(teaErrorMessage(err, t)),
  });

  const cancelTea = useMutation({
    mutationFn: (requestId: string) =>
      unwrap(api.POST('/me/tea-requests/{requestId}/cancel', { params: { path: { requestId } } })),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['tea-requests'] });
    },
  });

  const answer = useMutation({
    mutationFn: ({ matchId, accept }: { matchId: string; accept: boolean }) =>
      unwrap(api.POST('/me/matches/{matchId}/answer', { params: { path: { matchId } }, body: { accept } })),
    onSuccess: async () => {
      await client.invalidateQueries({ queryKey: ['matches'] });
    },
  });

  const formatLabel = (format: TeaFormat) => (format === 'meeting' ? t('tea.format.meeting') : t('tea.format.call'));

  return (
    <Screen title={t('title')} scroll={false}>
      <List<Match>
        data={matches.data}
        loading={matches.isLoading}
        error={matches.error}
        onRetry={() => void matches.refetch()}
        keyExtractor={(m) => m.id}
        renderItem={({ item }) => {
          const needsAnswer = item.status === 'proposed' && item.myAnswer === undefined;
          const busy = answer.isPending && answer.variables?.matchId === item.id;
          return (
            <Card testID={`match-${item.id}`}>
              <ListItem
                leading={<Avatar name={item.otherDisplayName} />}
                title={item.otherDisplayName}
                subtitle={matchSubtitle(item, t)}
                emphasis
              />
              {needsAnswer ? (
                <>
                  <Button
                    title={t('matches.accept')}
                    loading={busy && answer.variables?.accept === true}
                    disabled={busy}
                    onPress={() => answer.mutate({ matchId: item.id, accept: true })}
                    testID={`match-accept-${item.id}`}
                  />
                  <Button
                    title={t('matches.decline')}
                    variant="secondary"
                    loading={busy && answer.variables?.accept === false}
                    disabled={busy}
                    onPress={() => answer.mutate({ matchId: item.id, accept: false })}
                    testID={`match-decline-${item.id}`}
                  />
                </>
              ) : null}
              {item.roomId ? (
                <Button
                  title={t('matches.openChat')}
                  variant="secondary"
                  onPress={() => router.push(`/chat/${item.roomId}`)}
                  testID={`match-chat-${item.id}`}
                />
              ) : null}
            </Card>
          );
        }}
        empty={{ title: t('matches.emptyTitle'), description: t('matches.emptyDescription') }}
        header={
          <>
            <Card testID="circle-tea">
              <Heading>{t('tea.heading')}</Heading>
              <Text tone="muted">{t('tea.description')}</Text>
              {tea.isError ? <Banner tone="warning">{t('tea.errorGeneric')}</Banner> : null}
              {openTea ? (
                <>
                  <Text>
                    {t('tea.open', {
                      format: formatLabel(openTea.format),
                    })}
                  </Text>
                  <Button
                    title={t('tea.cancel')}
                    variant="secondary"
                    loading={cancelTea.isPending}
                    onPress={() => cancelTea.mutate(openTea.id)}
                    testID="tea-cancel"
                  />
                </>
              ) : (
                <Button title={t('tea.create')} onPress={() => setSheetOpen(true)} testID="tea-create" />
              )}
            </Card>
            <ListItem
              title={t('links.chats')}
              subtitle={t('links.chatsDescription')}
              onPress={() => router.push('/chat')}
              testID="link-chats"
            />
            <ListItem
              title={t('links.listings')}
              subtitle={t('links.listingsDescription')}
              onPress={() => router.push('/listings')}
              testID="link-listings"
            />
            <ListItem
              title={t('links.specialists')}
              subtitle={t('links.specialistsDescription')}
              onPress={() => router.push('/specialists')}
              testID="link-specialists"
            />
            <Heading>{t('matches.heading')}</Heading>
          </>
        }
        testID="circle-matches"
      />
      <Sheet visible={sheetOpen} onClose={() => setSheetOpen(false)} title={t('tea.sheetTitle')} testID="tea-sheet">
        <Text variant="label">{t('tea.format.label')}</Text>
        <Button
          title={t('tea.format.meeting')}
          variant={draft.format === 'meeting' ? 'primary' : 'secondary'}
          onPress={() => setDraft((d) => ({ ...d, format: 'meeting' }))}
          testID="tea-format-meeting"
        />
        <Button
          title={t('tea.format.call')}
          variant={draft.format === 'call' ? 'primary' : 'secondary'}
          onPress={() => setDraft((d) => ({ ...d, format: 'call' }))}
          testID="tea-format-call"
        />
        <Text variant="label">{t('tea.pref.heading')}</Text>
        <Button
          title={t(draft.sameDistrict ? 'tea.pref.sameDistrictOn' : 'tea.pref.sameDistrictOff')}
          variant="secondary"
          onPress={() => setDraft((d) => ({ ...d, sameDistrict: !d.sameDistrict }))}
          testID="tea-pref-district"
        />
        <Button
          title={t(draft.similarAge ? 'tea.pref.similarAgeOn' : 'tea.pref.similarAgeOff')}
          variant="secondary"
          onPress={() => setDraft((d) => ({ ...d, similarAge: !d.similarAge }))}
          testID="tea-pref-age"
        />
        <Button
          title={t(draft.sharedNeeds ? 'tea.pref.sharedNeedsOn' : 'tea.pref.sharedNeedsOff')}
          variant="secondary"
          onPress={() => setDraft((d) => ({ ...d, sharedNeeds: !d.sharedNeeds }))}
          testID="tea-pref-needs"
        />
        {teaError ? <Banner tone="danger">{teaError}</Banner> : null}
        <Button
          title={t('tea.submit')}
          loading={createTea.isPending}
          onPress={() => {
            setTeaError(null);
            createTea.mutate();
          }}
          testID="tea-submit"
        />
      </Sheet>
    </Screen>
  );
}
