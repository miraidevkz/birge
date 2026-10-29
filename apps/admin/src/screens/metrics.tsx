// Metrics: the pilot metrics of requirements § 9 for a period (getMetrics),
// admin only (ADR-0024). The tab is hidden from other roles, but the server
// decides: a 403 says "only an admin can see metrics", a 404/501 shows the
// calm "not on this server yet" state, and a 400 invalid_period explains the
// period rule. The picker offers presets and a custom range of UTC days; a
// custom range longer than 12 months is caught here before it is sent.
//
// Privacy: a cell from fewer than k members arrives as `suppressed: true`
// only. It is shown as the "hidden" marker, never as 0 or an empty number,
// and the bar view draws no bar for it. "Download CSV" fetches the same
// metrics as a file with the staff session and hands it to the browser; the
// data is never logged or cached.
import { useTranslation } from '@birge/i18n';
import {
  Banner,
  BarSeries,
  Button,
  Card,
  ChoiceGroup,
  EmptyState,
  ErrorState,
  Heading,
  ListItem,
  LoadingState,
  Screen,
  Text,
  TextField,
  type BarSeriesItem,
} from '@birge/ui';
import { ApiError } from '@birge/api';
import { useMutation } from '@tanstack/react-query';
import { useMemo, useState, type ReactNode } from 'react';

import {
  PERIOD_PRESETS,
  cellNumber,
  checkPeriod,
  fetchMetricsCsv,
  presetPeriod,
  todayUtc,
  useMetrics,
  type BucketCell,
  type CellFormat,
  type MetricCell,
  type Metrics,
  type Period,
  type PeriodPreset,
  type ScoreAggregate,
} from '@/lib/metrics';
import { isForbidden, isNotImplemented } from '@/lib/not-implemented';
import { saveTextFile } from '@/lib/save-file';

/** isInvalidPeriod reports the server's 400 for a period it does not serve. */
function isInvalidPeriod(err: unknown): boolean {
  return err instanceof ApiError && err.status === 400;
}

/** MetricsScreen shows the pilot metrics for a chosen period and downloads them as CSV. */
export function MetricsScreen() {
  const { t } = useTranslation('admin');
  const [today] = useState(() => todayUtc());
  const [preset, setPreset] = useState<PeriodPreset>('last4w');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');

  // A custom range is sent once both days are complete; while typing, the
  // fields show what is missing and nothing is requested.
  const complete = from.length >= 10 && to.length >= 10;
  const problem = preset === 'custom' && complete ? checkPeriod(from, to) : undefined;
  const period = useMemo<Period | undefined>(() => {
    if (preset !== 'custom') return presetPeriod(preset, today);
    return complete && problem === undefined ? { from, to } : undefined;
  }, [preset, today, from, to, complete, problem]);

  const metrics = useMetrics(period);
  const forbidden = isForbidden(metrics.error);
  const notAvailable = isNotImplemented(metrics.error);
  const invalidPeriod = isInvalidPeriod(metrics.error);

  const download = useMutation({
    mutationFn: async (p: Period) => {
      const csv = await fetchMetricsCsv(p);
      saveTextFile(csv.text, csv.filename, 'text/csv;charset=utf-8');
    },
  });

  if (forbidden || notAvailable) {
    return (
      <Screen title={t('metrics.title')}>
        {forbidden ? (
          <EmptyState title={t('metrics.forbiddenTitle')} description={t('metrics.forbiddenDescription')} />
        ) : (
          <EmptyState title={t('notAvailable.title')} description={t('notAvailable.description')} />
        )}
      </Screen>
    );
  }

  let body: ReactNode = null;
  if (invalidPeriod) {
    body = (
      <Banner tone="warning" testID="metrics-invalid-period">
        {t('metrics.period.invalid')}
      </Banner>
    );
  } else if (metrics.isLoading) {
    body = <LoadingState />;
  } else if (metrics.error) {
    body = <ErrorState onRetry={() => void metrics.refetch()} />;
  } else if (metrics.data) {
    body = <MetricsBody data={metrics.data} />;
  }

  let customError: string | undefined;
  if (problem) customError = t(`metrics.period.${problem}`);

  return (
    <Screen title={t('metrics.title')}>
      <Text tone="muted">{t('metrics.intro')}</Text>
      <ChoiceGroup
        label={t('metrics.period.label')}
        options={PERIOD_PRESETS.map((p) => ({ value: p, label: t(`metrics.period.${p}`) }))}
        value={preset}
        onChange={setPreset}
        testID="metrics-preset"
      />
      {preset === 'custom' ? (
        <>
          <TextField
            label={t('metrics.period.from')}
            hint={t('metrics.period.dayHint')}
            value={from}
            onChangeText={setFrom}
            autoCapitalize="none"
            testID="metrics-from"
          />
          <TextField
            label={t('metrics.period.to')}
            hint={t('metrics.period.dayHint')}
            error={customError}
            value={to}
            onChangeText={setTo}
            autoCapitalize="none"
            testID="metrics-to"
          />
        </>
      ) : null}
      {period ? <Text tone="muted">{t('metrics.period.shown', { from: period.from, to: period.to })}</Text> : null}
      <Button
        title={t('metrics.download')}
        variant="secondary"
        disabled={period === undefined}
        loading={download.isPending}
        onPress={() => period && download.mutate(period)}
        testID="metrics-download"
      />
      {download.isError && !isInvalidPeriod(download.error) ? (
        <Banner tone="warning" testID="metrics-download-failed">
          {t('metrics.downloadFailed')}
        </Banner>
      ) : null}
      {download.isError && isInvalidPeriod(download.error) ? (
        <Banner tone="warning">{t('metrics.period.invalid')}</Banner>
      ) : null}
      {body}
    </Screen>
  );
}

/** MetricsBody lays out the sections in the order of requirements § 9. */
function MetricsBody({ data }: { data: Metrics }) {
  const { t } = useTranslation('admin');
  const k = data.k;
  const row = (id: string, label: string, cell: MetricCell, format: CellFormat) => (
    <MetricRow key={id} id={id} label={label} cell={cell} format={format} k={k} />
  );
  return (
    <>
      <Text tone="muted" testID="metrics-k">
        {k <= 1 ? t('metrics.kNoteEmpty') : t('metrics.kNote', { k })}
      </Text>

      <Section id="activity">
        <Series id="weekly" label={t('metrics.activity.weekly')} buckets={data.activity.weekly} k={k} />
        <Series id="monthly" label={t('metrics.activity.monthly')} buckets={data.activity.monthly} k={k} />
      </Section>

      <Section id="retention">
        {row('retention4w', t('metrics.retention.w4'), data.activity.retention4w, 'share')}
        {row('retention8w', t('metrics.retention.w8'), data.activity.retention8w, 'share')}
      </Section>

      <Section id="tea">
        {row('tea-requests', t('metrics.tea.requests'), data.tea.requests, 'count')}
        {row('tea-pairs', t('metrics.tea.pairs'), data.tea.pairs, 'count')}
        {row('tea-meetings', t('metrics.tea.meetings'), data.tea.meetings, 'count')}
      </Section>

      <Section id="sos">
        {row('sos-raised', t('metrics.sos.raised'), data.sos.raised, 'count')}
        {row('sos-closed', t('metrics.sos.closedShare'), data.sos.closedShare, 'share')}
        {row('sos-first', t('metrics.sos.firstResponse'), data.sos.firstResponseMedianMinutes, 'minutes')}
      </Section>

      <Section id="escalations">
        {row('esc-opened', t('metrics.escalations.opened'), data.escalations.opened, 'count')}
        {row('esc-taken', t('metrics.escalations.taken15'), data.escalations.takenWithin15mShare, 'share')}
      </Section>

      <Section id="rights">
        {row('rights-answered', t('metrics.rights.answered'), data.rights.answeredFromKnowledge, 'count')}
        {row('rights-helped', t('metrics.rights.helped'), data.rights.helpedShare, 'share')}
      </Section>

      <Section id="stress">
        <Score id="stress-baseline" label={t('metrics.score.baseline')} score={data.stress.baseline} k={k} />
        <Score id="stress-week8" label={t('metrics.score.week8')} score={data.stress.week8} k={k} />
      </Section>

      <Section id="isolation">
        <Score id="isolation-before" label={t('metrics.score.before')} score={data.isolation.before} k={k} />
        <Score id="isolation-after" label={t('metrics.score.after')} score={data.isolation.after} k={k} />
      </Section>
    </>
  );
}

type SectionId = 'activity' | 'retention' | 'tea' | 'sos' | 'escalations' | 'rights' | 'stress' | 'isolation';

/** Section is one card: title, a line on what it counts (ADR-0024), then its rows. */
function Section({ id, children }: { id: SectionId; children: ReactNode }) {
  const { t } = useTranslation('admin');
  return (
    <Card testID={`metrics-${id}`}>
      <Heading>{t(`metrics.section.${id}.title`)}</Heading>
      <Text tone="muted">{t(`metrics.section.${id}.about`)}</Text>
      {children}
    </Card>
  );
}

/**
 * MetricRow is one aggregate: its value with how many members it is built
 * from, or the "hidden" marker for a suppressed cell (no number at all).
 */
function MetricRow(props: { id: string; label: string; cell: MetricCell; format: CellFormat; k: number }) {
  const { id, label, cell, format, k } = props;
  const { t } = useTranslation('admin');
  const n = cellNumber(cell, format);
  if (n === undefined) {
    const marker = k <= 1 ? t('metrics.suppressedEmpty') : t('metrics.suppressed', { k });
    return <ListItem title={label} subtitle={marker} testID={`metrics-row-${id}`} />;
  }
  let value = String(n);
  if (format === 'share') value = t('metrics.value.percent', { value: n });
  if (format === 'minutes') value = t('metrics.value.minutes', { value: n });
  const members = cell.members === undefined ? undefined : t('metrics.members', { members: cell.members });
  return (
    <ListItem
      title={label}
      subtitle={members}
      accessibilityLabel={members ? `${label}: ${value}, ${members}` : `${label}: ${value}`}
      trailing={<Text variant="label">{value}</Text>}
      testID={`metrics-row-${id}`}
    />
  );
}

/** Series shows weekly or monthly activity as bars; a suppressed bucket has no bar, only the marker. */
function Series({ id, label, buckets, k }: { id: string; label: string; buckets: BucketCell[]; k: number }) {
  const { t } = useTranslation('admin');
  if (buckets.length === 0) {
    return (
      <>
        <Text variant="label">{label}</Text>
        <Text tone="muted">{t('metrics.activity.empty')}</Text>
      </>
    );
  }
  const items: BarSeriesItem[] = buckets.map((b) => {
    const n = cellNumber(b.cell, 'count');
    const bucket = id === 'weekly' ? t('metrics.activity.weekOf', { date: b.bucket }) : b.bucket.slice(0, 7);
    return {
      key: b.bucket,
      label: bucket,
      value: n,
      valueLabel:
        n === undefined
          ? k <= 1
            ? t('metrics.suppressedShortEmpty')
            : t('metrics.suppressedShort', { k })
          : String(n),
    };
  });
  return <BarSeries label={label} items={items} testID={`metrics-${id}`} />;
}

/**
 * Score is one side of a score comparison (baseline or week 8, before or
 * after): members, mean and median. When both mean and median are hidden
 * one marker stands for the whole group instead of three.
 */
function Score({ id, label, score, k }: { id: string; label: string; score: ScoreAggregate; k: number }) {
  const { t } = useTranslation('admin');
  const hidden = cellNumber(score.mean, 'score') === undefined && cellNumber(score.median, 'score') === undefined;
  if (hidden) {
    return (
      <ListItem
        title={label}
        subtitle={k <= 1 ? t('metrics.suppressedEmpty') : t('metrics.suppressed', { k })}
        testID={`metrics-row-${id}`}
      />
    );
  }
  const members: MetricCell =
    score.mean.members === undefined ? { suppressed: true } : { suppressed: false, value: score.mean.members };
  return (
    <>
      <Text variant="label">{label}</Text>
      <MetricRow id={`${id}-members`} label={t('metrics.score.members')} cell={members} format="count" k={k} />
      <MetricRow id={`${id}-mean`} label={t('metrics.score.mean')} cell={score.mean} format="score" k={k} />
      <MetricRow id={`${id}-median`} label={t('metrics.score.median')} cell={score.median} format="score" k={k} />
    </>
  );
}
