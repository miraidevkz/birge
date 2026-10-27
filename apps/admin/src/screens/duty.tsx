// Duty roster for the current ISO week. Phone numbers for new shifts are sent
// once and never shown again (encrypted server-side).
import { unwrap, type Schemas } from '@birge/api';
import { useTranslation } from '@birge/i18n';
import { Banner, Button, Card, ChoiceGroup, List, Screen, Sheet, Text, TextField } from '@birge/ui';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useMemo, useState } from 'react';

import { api } from '@/lib/api';

type DutyShift = Schemas['DutyShift'];
type Role = DutyShift['role'];

const ROLES: Role[] = ['psychologist', 'moderator'];
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const E164 = /^\+[1-9][0-9]{7,14}$/;

/** currentWeekRange returns Monday 00:00 UTC to next Monday 00:00 UTC. */
export function currentWeekRange(now = new Date()): { from: string; to: string; label: string } {
  const day = now.getUTCDay(); // 0 = Sunday
  const daysFromMonday = (day + 6) % 7;
  const monday = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate() - daysFromMonday));
  const next = new Date(monday);
  next.setUTCDate(monday.getUTCDate() + 7);
  const from = monday.toISOString();
  const to = next.toISOString();
  return { from, to, label: monday.toISOString().slice(0, 10) };
}

/** DutyScreen shows and edits the current week's duty shifts. */
export function DutyScreen() {
  const { t } = useTranslation('admin');
  const client = useQueryClient();
  const week = useMemo(() => currentWeekRange(), []);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [staffId, setStaffId] = useState('');
  const [role, setRole] = useState<Role>('psychologist');
  const [startsAt, setStartsAt] = useState('');
  const [endsAt, setEndsAt] = useState('');
  const [phone, setPhone] = useState('');
  const [backup, setBackup] = useState<'primary' | 'backup'>('primary');
  const [invalid, setInvalid] = useState(false);
  const [actionError, setActionError] = useState(false);

  const roster = useQuery({
    queryKey: ['staff', 'duty', week.from, week.to],
    queryFn: () =>
      unwrap(
        api.GET('/staff/duty-roster', {
          params: { query: { from: week.from, to: week.to } },
        }),
      ),
  });

  const add = useMutation({
    mutationFn: () =>
      unwrap(
        api.POST('/staff/duty-roster', {
          body: {
            staffId: staffId.trim(),
            role,
            startsAt: startsAt.trim(),
            endsAt: endsAt.trim(),
            backup: backup === 'backup',
            phone: phone.trim(),
          },
        }),
      ),
    onSuccess: async () => {
      setActionError(false);
      setSheetOpen(false);
      setStaffId('');
      setStartsAt('');
      setEndsAt('');
      setPhone('');
      setBackup('primary');
      await client.invalidateQueries({ queryKey: ['staff', 'duty'] });
    },
    onError: () => setActionError(true),
  });

  const remove = useMutation({
    mutationFn: (shiftId: string) =>
      unwrap(api.DELETE('/staff/duty-roster/{shiftId}', { params: { path: { shiftId } } })),
    onSuccess: async () => {
      setActionError(false);
      await client.invalidateQueries({ queryKey: ['staff', 'duty'] });
    },
    onError: () => setActionError(true),
  });

  function submit() {
    if (!UUID.test(staffId.trim()) || !E164.test(phone.trim()) || startsAt.trim() === '' || endsAt.trim() === '') {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    add.mutate();
  }

  return (
    <Screen title={t('duty.title')} scroll={false}>
      <List<DutyShift>
        data={roster.data}
        loading={roster.isLoading}
        error={roster.error}
        onRetry={() => void roster.refetch()}
        keyExtractor={(item) => item.id}
        empty={{ title: t('duty.emptyTitle'), description: t('duty.emptyDescription') }}
        testID="duty"
        header={
          <>
            <Text>{t('duty.week', { from: week.label })}</Text>
            {actionError ? <Banner tone="danger">{t('signIn.failed')}</Banner> : null}
            <Button title={t('duty.add')} onPress={() => setSheetOpen(true)} testID="duty-add" />
          </>
        }
        renderItem={({ item }) => (
          <Card testID={`shift-${item.id}`}>
            <Text variant="label">{t(`duty.role.${item.role}`)}</Text>
            <Text tone="muted">
              {item.startsAt} → {item.endsAt}
            </Text>
            <Text tone="muted">{item.backup ? t('duty.backup') : t('duty.primary')}</Text>
            <Text tone="muted">{item.staffId}</Text>
            <Button
              title={t('duty.delete')}
              variant="destructive"
              onPress={() => remove.mutate(item.id)}
              loading={remove.isPending && remove.variables === item.id}
              testID={`delete-${item.id}`}
            />
          </Card>
        )}
      />
      <Sheet visible={sheetOpen} onClose={() => setSheetOpen(false)} title={t('duty.addTitle')} testID="duty-sheet">
        <TextField
          label={t('duty.staffId.label')}
          hint={t('duty.staffId.hint')}
          value={staffId}
          onChangeText={setStaffId}
          autoCapitalize="none"
          testID="duty-staff-id"
        />
        <ChoiceGroup
          label={t('duty.role.label')}
          options={ROLES.map((r) => ({ value: r, label: t(`duty.role.${r}`) }))}
          value={role}
          onChange={setRole}
          testID="duty-role"
        />
        <TextField
          label={t('duty.startsAt.label')}
          hint={t('duty.startsAt.hint')}
          value={startsAt}
          onChangeText={setStartsAt}
          autoCapitalize="none"
          testID="duty-starts"
        />
        <TextField
          label={t('duty.endsAt.label')}
          hint={t('duty.endsAt.hint')}
          value={endsAt}
          onChangeText={setEndsAt}
          autoCapitalize="none"
          testID="duty-ends"
        />
        <TextField
          label={t('duty.phone.label')}
          hint={t('duty.phone.hint')}
          value={phone}
          onChangeText={setPhone}
          keyboardType="phone-pad"
          testID="duty-phone"
        />
        <ChoiceGroup
          label={t('duty.backupChoice.label')}
          options={[
            { value: 'primary', label: t('duty.backupChoice.primary') },
            { value: 'backup', label: t('duty.backupChoice.backup') },
          ]}
          value={backup}
          onChange={setBackup}
          testID="duty-backup"
        />
        {invalid ? <Banner tone="danger">{t('duty.invalid')}</Banner> : null}
        <Button title={t('duty.confirmAdd')} onPress={submit} loading={add.isPending} testID="duty-save" />
      </Sheet>
    </Screen>
  );
}
