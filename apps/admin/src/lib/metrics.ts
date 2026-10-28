// Pilot metrics (ADR-0024): the period rules, the reads of GET /staff/metrics
// and GET /staff/metrics.csv, and the number formats the Metrics screen uses.
//
// Privacy: a cell built from fewer than k members comes with only
// `suppressed: true` (no members, no value). Nothing here turns such a cell
// into a number: formatCell returns undefined for it and the screen shows
// the "hidden" marker instead. Both reads are audited by the server, so
// nothing refetches on a timer or on window focus, and neither the JSON nor
// the CSV is ever logged or put into a query key (the key is the period).
import { NetworkError, toApiError, unwrap, type Schemas } from '@birge/api';
import { useQuery } from '@tanstack/react-query';

import { api } from './api';
import { isDay } from './time';

/** Metrics is the response of getMetrics. */
export type Metrics = Schemas['Metrics'];

/** MetricCell is one aggregate; suppressed cells carry no members and no value. */
export type MetricCell = Schemas['MetricCell'];

/** ScoreAggregate is the mean and median of a score (stress, isolation). */
export type ScoreAggregate = Schemas['ScoreAggregate'];

/** BucketCell is one week or month of a series. */
export type BucketCell = Schemas['BucketCell'];

/** Period is an inclusive range of calendar days, YYYY-MM-DD. */
export interface Period {
  from: string;
  to: string;
}

/** PeriodPreset is a quick choice of period; custom takes the typed days. */
export type PeriodPreset = 'last4w' | 'last3m' | 'ytd' | 'custom';

/** PERIOD_PRESETS lists the presets in the order the picker shows them. */
export const PERIOD_PRESETS: readonly PeriodPreset[] = ['last4w', 'last3m', 'ytd', 'custom'];

/** MAX_PERIOD_MONTHS is the server's limit: to is at most 12 months after from (analytics model). */
export const MAX_PERIOD_MONTHS = 12;

/** PeriodProblem is why a typed period is not sent. */
export type PeriodProblem = 'dayInvalid' | 'reversed' | 'tooLong';

function parts(day: string): [number, number, number] {
  const [y, m, d] = day.split('-').map(Number);
  return [y ?? 0, (m ?? 1) - 1, d ?? 1];
}

function toDay(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10);
}

/** todayUtc returns today's UTC date; the console shows and sends days in UTC (lib/time). */
export function todayUtc(now: Date = new Date()): string {
  return now.toISOString().slice(0, 10);
}

/** addDays moves a YYYY-MM-DD day by n days. */
export function addDays(day: string, n: number): string {
  const [y, m, d] = parts(day);
  return toDay(Date.UTC(y, m, d + n));
}

/**
 * addMonths moves a YYYY-MM-DD day by n calendar months. A day that does not
 * exist in the target month rolls over (Jan 31 + 1 month = Mar 3 or 2), the
 * same normalisation as Go's time.AddDate, so the 12-month check here agrees
 * with the server's.
 */
export function addMonths(day: string, n: number): string {
  const [y, m, d] = parts(day);
  return toDay(Date.UTC(y, m + n, d));
}

/** presetPeriod returns the days of a preset ending today (custom has none: undefined). */
export function presetPeriod(preset: PeriodPreset, today: string): Period | undefined {
  switch (preset) {
    case 'last4w':
      return { from: addDays(today, -27), to: today };
    case 'last3m':
      return { from: addDays(addMonths(today, -3), 1), to: today };
    case 'ytd':
      return { from: `${today.slice(0, 4)}-01-01`, to: today };
    default:
      return undefined;
  }
}

/**
 * checkPeriod returns why a typed period cannot be sent, or undefined when it
 * can: both days real, to not before from, to at most 12 months after from.
 */
export function checkPeriod(from: string, to: string): PeriodProblem | undefined {
  if (!isDay(from) || !isDay(to)) return 'dayInvalid';
  if (to < from) return 'reversed';
  if (to > addMonths(from, MAX_PERIOD_MONTHS)) return 'tooLong';
  return undefined;
}

/** metricsKey is the query key prefix of the metrics read; it carries the period only. */
export const metricsKey = ['analytics', 'metrics'] as const;

/**
 * useMetrics reads GET /staff/metrics for a period, or nothing while period
 * is undefined (a custom period not complete yet). Every read is audited, so
 * it is not refetched on focus.
 */
export function useMetrics(period: Period | undefined) {
  return useQuery({
    queryKey: [...metricsKey, period?.from ?? '', period?.to ?? ''],
    queryFn: () =>
      unwrap(api.GET('/staff/metrics', { params: { query: { from: period?.from ?? '', to: period?.to ?? '' } } })),
    enabled: period !== undefined,
    refetchOnWindowFocus: false,
  });
}

/** MetricsCsv is a downloaded export: its text and the name to save it under. */
export interface MetricsCsv {
  text: string;
  filename: string;
}

const SAFE_NAME = /^[A-Za-z0-9._-]{1,120}$/;

/**
 * csvFilename takes the file name from the server's Content-Disposition
 * (`attachment; filename="birge-metrics-<from>-<to>.csv"`). A missing or odd
 * name (anything beyond letters, digits, dot, dash and underscore) falls back
 * to the same pattern built from the period, so a header can never steer
 * where or as what the file is saved.
 */
export function csvFilename(disposition: string | null, period: Period): string {
  const match = disposition ? /filename="?([^";]+)"?/i.exec(disposition) : null;
  const name = match?.[1]?.trim();
  if (name && SAFE_NAME.test(name)) return name;
  return `birge-metrics-${period.from}-${period.to}.csv`;
}

/**
 * fetchMetricsCsv reads GET /staff/metrics.csv with the staff session. It
 * throws ApiError or NetworkError like unwrap; unwrap itself cannot be used
 * because the file name is in a response header.
 */
export async function fetchMetricsCsv(period: Period): Promise<MetricsCsv> {
  let result;
  try {
    result = await api.GET('/staff/metrics.csv', { params: { query: period }, parseAs: 'text' });
  } catch (cause) {
    throw new NetworkError(cause);
  }
  if (!result.response.ok) throw toApiError(result.response.status, result.error);
  return {
    text: typeof result.data === 'string' ? result.data : '',
    filename: csvFilename(result.response.headers.get('Content-Disposition'), period),
  };
}

/** CellFormat is how a cell's value reads: a count, a share (0..1), minutes or a score. */
export type CellFormat = 'count' | 'share' | 'minutes' | 'score';

/**
 * cellNumber returns the number to show for a cell in its format, or
 * undefined when the cell is suppressed or carries no value. A share becomes
 * whole percent, minutes and counts whole numbers, a score one decimal.
 */
export function cellNumber(cell: MetricCell | undefined, format: CellFormat): number | undefined {
  if (!cell || cell.suppressed || cell.value === undefined) return undefined;
  switch (format) {
    case 'share':
      return Math.round(cell.value * 100);
    case 'score':
      return Math.round(cell.value * 10) / 10;
    default:
      return Math.round(cell.value);
  }
}
