// Members: verified or suspended members, for mentor assignment and
// suspension review (staffListMembers). First name and district only; every
// read is audited by the server. Names stay on screen: never logged, never in
// a query key. Lifting a suspension is admin only; the client does not guess
// the role and shows the admin-only text when the API answers 403.
import { ApiError, unwrap } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ChoiceGroup, ConfirmSheet, List, Screen, Text } from '@birge/ui';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';

import { api } from '@/lib/api';
import { membersKey, useDistricts, useStaffMembers, type MemberStatus, type StaffMember } from '@/lib/members';
import { isNotImplemented } from '@/lib/not-implemented';
import { formatDate } from '@/lib/time';

const STATUSES: MemberStatus[] = ['verified', 'suspended'];
const ALL = 'all';

type Notice = 'lifted' | 'forbidden' | 'failed';

/** MembersScreen lists verified and suspended members and lets an admin lift a suspension. */
export function MembersScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const [status, setStatus] = useState<MemberStatus>('verified');
  const [districtId, setDistrictId] = useState<string>(ALL);
  const [lifting, setLifting] = useState<StaffMember | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);

  const members = useStaffMembers(status, districtId === ALL ? undefined : districtId);
  const districts = useDistricts();
  const notAvailable = isNotImplemented(members.error);

  const lift = useMutation({
    mutationFn: (memberId: string) =>
      unwrap(api.DELETE('/staff/members/{memberId}/suspension', { params: { path: { memberId } } })),
    onSuccess: async () => {
      setLifting(null);
      setNotice('lifted');
      await client.invalidateQueries({ queryKey: membersKey });
    },
    onError: (err) => {
      setLifting(null);
      // mfa_required is a 403 too, but the session layer handles it (sign-in again).
      const forbidden = err instanceof ApiError && err.status === 403 && err.code !== 'mfa_required';
      setNotice(forbidden ? 'forbidden' : 'failed');
    },
  });

  const noticeBanner = () => {
    switch (notice) {
      case 'lifted':
        return <Banner tone="success">{t('members.liftDone')}</Banner>;
      case 'forbidden':
        return <Banner tone="warning">{t('members.liftForbidden')}</Banner>;
      case 'failed':
        return <Banner tone="danger">{t('actions.failed')}</Banner>;
      default:
        return null;
    }
  };

  const emptyState = () => {
    if (notAvailable) return { title: t('notAvailable.title'), description: t('notAvailable.description') };
    if (status === 'verified') {
      return { title: t('members.emptyVerifiedTitle'), description: t('members.emptyVerifiedDescription') };
    }
    return { title: t('members.emptySuspendedTitle'), description: t('members.emptySuspendedDescription') };
  };

  // A 501 is not an error: the list simply does not exist on this server yet.
  const rows = notAvailable ? [] : members.data?.pages.flatMap((p) => p.items);

  return (
    <Screen title={t('members.title')} scroll={false}>
      <List<StaffMember>
        data={rows}
        loading={members.isLoading}
        error={notAvailable ? undefined : members.error}
        onRetry={() => void members.refetch()}
        keyExtractor={(m) => m.id}
        empty={emptyState()}
        testID="members"
        onLoadMore={members.hasNextPage ? () => void members.fetchNextPage() : undefined}
        loadingMore={members.isFetchingNextPage}
        header={
          <>
            <ChoiceGroup
              label={t('members.status.label')}
              options={STATUSES.map((s) => ({ value: s, label: t(`members.status.${s}`) }))}
              value={status}
              onChange={(s) => {
                setStatus(s);
                setNotice(null);
              }}
              testID="members-status"
            />
            {districts.options.length > 0 && !notAvailable ? (
              <ChoiceGroup
                label={t('members.district.label')}
                options={[
                  { value: ALL, label: t('members.district.all') },
                  ...districts.options.map((d) => ({ value: d.id, label: d.label })),
                ]}
                value={districtId}
                onChange={setDistrictId}
                testID="members-district"
              />
            ) : null}
            {noticeBanner()}
          </>
        }
        renderItem={({ item }) => {
          const district = districts.nameOf(item.districtId);
          return (
            <Card testID={`member-${item.id}`}>
              <Text variant="label">{item.displayName}</Text>
              {district ? <Text tone="muted">{district}</Text> : null}
              {item.verifiedAt ? (
                <Text tone="muted">{t('members.verifiedOn', { date: formatDate(item.verifiedAt) })}</Text>
              ) : null}
              {item.suspendedAt ? (
                <Text tone="muted">{t('members.suspendedOn', { date: formatDate(item.suspendedAt) })}</Text>
              ) : null}
              {item.suspended ? (
                <Button
                  title={t('members.lift')}
                  variant="secondary"
                  onPress={() => {
                    setNotice(null);
                    setLifting(item);
                  }}
                  testID={`lift-${item.id}`}
                />
              ) : null}
            </Card>
          );
        }}
      />
      <ConfirmSheet
        visible={lifting !== null}
        title={t('members.liftTitle')}
        message={t('members.liftMessage')}
        confirmLabel={t('members.lift')}
        onConfirm={() => {
          if (lifting) lift.mutate(lifting.id);
        }}
        onClose={() => setLifting(null)}
        loading={lift.isPending}
        testID="lift-confirm"
      />
    </Screen>
  );
}
