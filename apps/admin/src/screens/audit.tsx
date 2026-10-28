// Audit: the log of staff actions, newest first (listAuditEntries), admin
// only. The tab is hidden from other roles, but the server is the authority:
// a 403 shows "only an admin can see this". Filters: person (from the staff
// accounts), action and object type (chips over the codes the console
// knows), and a UTC date range. Entries carry codes and IDs only; an unknown
// code shows a generic label with the code beneath it. Tapping the person
// opens their staff card; the object opens the tab that shows it, when the
// console has one (lib/audit-links). Staff names stay on screen only.
import { useTranslation } from '@birge/i18n';
import { Button, Card, ChoiceGroup, List, Screen, Text, TextField } from '@birge/ui';
import { router, useLocalSearchParams } from 'expo-router';
import { useMemo, useState } from 'react';

import {
  AUDIT_ACTIONS,
  AUDIT_OBJECTS,
  auditLabelKey,
  auditRoleKey,
  shortId,
  type AuditCodeKind,
} from '@/lib/audit-codes';
import { auditObjectLink } from '@/lib/audit-links';
import { isForbidden, isNotImplemented } from '@/lib/not-implemented';
import { staffName, useAuditEntries, useStaffAccounts, type AuditEntry, type AuditFilter } from '@/lib/staff';
import { dayEnd, dayStart, formatDateTime, isDay } from '@/lib/time';

const ALL = 'all';

/** AuditScreen lists staff actions with person, action, object and date filters. */
export function AuditScreen() {
  const { t } = useTranslation('admin');
  const params = useLocalSearchParams<{ actorId?: string }>();
  const paramActor = typeof params.actorId === 'string' && params.actorId !== '' ? params.actorId : ALL;

  const [actorId, setActorId] = useState(paramActor);
  // "Show their actions" on a staff card returns here with a new actorId:
  // the param wins over the chip chosen before (state derived from props).
  const [seenParam, setSeenParam] = useState(paramActor);
  if (paramActor !== seenParam) {
    setSeenParam(paramActor);
    setActorId(paramActor);
  }
  const [action, setAction] = useState<string>(ALL);
  const [objectType, setObjectType] = useState<string>(ALL);
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');

  // A day filter applies once it is a complete, valid date; while typing the
  // previous result stays and no error flashes.
  const fromValid = isDay(from);
  const toValid = isDay(to);
  const rangeReversed = fromValid && toValid && from > to;
  const fromError = from.length >= 10 && !fromValid ? t('audit.filter.dayInvalid') : undefined;
  let toError: string | undefined;
  if (to.length >= 10 && !toValid) toError = t('audit.filter.dayInvalid');
  else if (rangeReversed) toError = t('audit.filter.rangeInvalid');

  const filter = useMemo<AuditFilter>(() => {
    const f: AuditFilter = {};
    if (actorId !== ALL) f.actorId = actorId;
    if (action !== ALL) f.action = action;
    if (objectType !== ALL) f.objectType = objectType;
    if (fromValid && !rangeReversed) f.from = dayStart(from);
    if (toValid && !rangeReversed) f.to = dayEnd(to);
    return f;
  }, [actorId, action, objectType, from, to, fromValid, toValid, rangeReversed]);

  const entries = useAuditEntries(filter);
  const accounts = useStaffAccounts();
  const notAvailable = isNotImplemented(entries.error);
  const forbidden = isForbidden(entries.error);
  const calm = notAvailable || forbidden;

  const label = (kind: AuditCodeKind, code: string) => {
    const key = auditLabelKey(kind, code);
    return key ? t(key) : t(`audit.unknown.${kind}`);
  };

  // A role the console does not know (one the contract has not listed yet)
  // gets the generic label; the raw role is shown beneath it.
  const roleLabel = (role: string) => {
    const key = auditRoleKey(role);
    return key ? t(key) : t('audit.unknown.role');
  };

  const personOptions = useMemo(() => {
    const list = accounts.data?.items ?? [];
    const options = [
      { value: ALL, label: t('audit.filter.allPeople') },
      ...list.map((a) => ({ value: a.id, label: staffName(a.id, a.displayName) })),
    ];
    // Opened from a card whose person is not in the list (or the list is not served yet).
    if (actorId !== ALL && !list.some((a) => a.id === actorId)) {
      options.push({ value: actorId, label: shortId(actorId) });
    }
    return options;
  }, [accounts.data, actorId, t]);

  // The route param follows the chip, so a later "Show their actions" for
  // the same person still counts as a change and applies.
  const chooseActor = (value: string) => {
    setActorId(value);
    router.setParams({ actorId: value === ALL ? undefined : value });
  };

  const emptyState = () => {
    if (forbidden) return { title: t('audit.forbiddenTitle'), description: t('audit.forbiddenDescription') };
    if (notAvailable) return { title: t('notAvailable.title'), description: t('notAvailable.description') };
    return { title: t('audit.emptyTitle'), description: t('audit.emptyDescription') };
  };

  // A 501, an unmounted route or a 403 is not an error banner: the list
  // does not exist for this person or on this server.
  const rows = calm ? [] : entries.data?.pages.flatMap((p) => p.items);

  return (
    <Screen title={t('audit.title')} scroll={false}>
      <List<AuditEntry>
        data={rows}
        loading={entries.isLoading}
        error={calm ? undefined : entries.error}
        onRetry={() => void entries.refetch()}
        keyExtractor={(e) => e.id}
        empty={emptyState()}
        testID="audit"
        onLoadMore={entries.hasNextPage && !calm ? () => void entries.fetchNextPage() : undefined}
        loadingMore={entries.isFetchingNextPage}
        header={
          <>
            <Button
              title={t('audit.openStaff')}
              variant="secondary"
              onPress={() => router.push('/audit/staff')}
              testID="audit-open-staff"
            />
            {forbidden ? null : (
              <>
                <ChoiceGroup
                  label={t('audit.filter.person')}
                  options={personOptions}
                  value={actorId}
                  onChange={chooseActor}
                  testID="audit-person"
                />
                <ChoiceGroup
                  label={t('audit.filter.action')}
                  options={[
                    { value: ALL, label: t('audit.filter.all') },
                    ...AUDIT_ACTIONS.map((c) => ({ value: c, label: label('action', c) })),
                  ]}
                  value={action}
                  onChange={setAction}
                  testID="audit-action"
                />
                <ChoiceGroup
                  label={t('audit.filter.object')}
                  options={[
                    { value: ALL, label: t('audit.filter.all') },
                    ...AUDIT_OBJECTS.map((c) => ({ value: c, label: label('object', c) })),
                  ]}
                  value={objectType}
                  onChange={setObjectType}
                  testID="audit-object"
                />
                <TextField
                  label={t('audit.filter.from')}
                  hint={t('audit.filter.dayHint')}
                  error={fromError}
                  value={from}
                  onChangeText={setFrom}
                  autoCapitalize="none"
                  testID="audit-from"
                />
                <TextField
                  label={t('audit.filter.to')}
                  hint={t('audit.filter.dayHint')}
                  error={toError}
                  value={to}
                  onChangeText={setTo}
                  autoCapitalize="none"
                  testID="audit-to"
                />
              </>
            )}
          </>
        }
        renderItem={({ item }) => {
          const actionKnown = auditLabelKey('action', item.action) !== undefined;
          const objectKnown = auditLabelKey('object', item.objectType) !== undefined;
          const reasonKnown = item.reason ? auditLabelKey('reason', item.reason) !== undefined : true;
          const object = label('object', item.objectType);
          const link = auditObjectLink(item.objectType);
          return (
            <Card testID={`audit-${item.id}`}>
              <Text variant="label">{label('action', item.action)}</Text>
              {actionKnown ? null : <Text tone="muted">{item.action}</Text>}
              <Text tone="muted">{formatDateTime(item.occurredAt)}</Text>
              <Text>
                {item.objectId ? t('audit.row.objectWithId', { object, id: shortId(item.objectId) }) : object}
              </Text>
              {objectKnown ? null : <Text tone="muted">{item.objectType}</Text>}
              {item.reason ? (
                <Text tone="muted">{t('audit.row.reason', { reason: label('reason', item.reason) })}</Text>
              ) : null}
              {item.reason && !reasonKnown ? <Text tone="muted">{item.reason}</Text> : null}
              <Text tone="muted">{t('audit.row.role', { role: roleLabel(item.actorRole) })}</Text>
              {auditRoleKey(item.actorRole) ? null : <Text tone="muted">{item.actorRole}</Text>}
              <Button
                title={staffName(item.actorId, item.actorName)}
                variant="secondary"
                size="sm"
                accessibilityHint={t('audit.row.openCard')}
                onPress={() => router.push({ pathname: '/audit/staff/[staffId]', params: { staffId: item.actorId } })}
                testID={`audit-actor-${item.id}`}
              />
              {link ? (
                <Button
                  title={t('audit.row.openObject', { screen: t(`tabs.${link.tab}`) })}
                  variant="secondary"
                  size="sm"
                  onPress={() => router.navigate(link.href)}
                  testID={`audit-object-${item.id}`}
                />
              ) : null}
            </Card>
          );
        }}
      />
    </Screen>
  );
}
