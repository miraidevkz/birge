import { describe, expect, it, jest } from '@jest/globals';

import { addMonths, cellNumber, checkPeriod, csvFilename, presetPeriod, todayUtc } from './metrics';

// metrics.ts also holds the queries; the pure helpers tested here need no client.
jest.mock('./api', () => ({ api: {} }));

describe('periods', () => {
  it('builds the presets ending today', () => {
    expect(presetPeriod('last4w', '2026-10-28')).toEqual({ from: '2026-10-01', to: '2026-10-28' });
    expect(presetPeriod('last3m', '2026-10-28')).toEqual({ from: '2026-07-29', to: '2026-10-28' });
    expect(presetPeriod('ytd', '2026-10-28')).toEqual({ from: '2026-01-01', to: '2026-10-28' });
    expect(presetPeriod('custom', '2026-10-28')).toBeUndefined();
    expect(todayUtc(new Date('2026-10-28T23:30:00Z'))).toBe('2026-10-28');
  });

  it('rolls a missing day over like Go AddDate', () => {
    expect(addMonths('2028-02-29', 12)).toBe('2029-03-01');
    expect(addMonths('2026-01-31', 1)).toBe('2026-03-03');
  });

  it('allows up to 12 months and names what is wrong otherwise', () => {
    expect(checkPeriod('2025-10-28', '2026-10-28')).toBeUndefined();
    expect(checkPeriod('2026-10-28', '2026-10-28')).toBeUndefined();
    expect(checkPeriod('2025-10-28', '2026-10-29')).toBe('tooLong');
    expect(checkPeriod('2026-10-10', '2026-10-01')).toBe('reversed');
    expect(checkPeriod('2026-02-30', '2026-03-01')).toBe('dayInvalid');
    expect(checkPeriod('2026-02-01', '')).toBe('dayInvalid');
  });
});

describe('cellNumber', () => {
  it('never turns a suppressed cell into a number', () => {
    expect(cellNumber({ suppressed: true }, 'count')).toBeUndefined();
    expect(cellNumber({ suppressed: true, value: 3 }, 'count')).toBeUndefined();
    expect(cellNumber({ suppressed: false }, 'share')).toBeUndefined();
    expect(cellNumber(undefined, 'share')).toBeUndefined();
  });

  it('formats shares as whole percent, scores with one decimal, counts and minutes whole', () => {
    expect(cellNumber({ suppressed: false, value: 0.625 }, 'share')).toBe(63);
    expect(cellNumber({ suppressed: false, value: 0 }, 'count')).toBe(0);
    expect(cellNumber({ suppressed: false, value: 8.25 }, 'score')).toBe(8.3);
    expect(cellNumber({ suppressed: false, value: 4.4 }, 'minutes')).toBe(4);
  });
});

describe('csvFilename', () => {
  const period = { from: '2026-10-01', to: '2026-10-28' };

  it('takes the server file name', () => {
    expect(csvFilename('attachment; filename="birge-metrics-2026-10-01-2026-10-28.csv"', period)).toBe(
      'birge-metrics-2026-10-01-2026-10-28.csv',
    );
    expect(csvFilename('attachment; filename=report.csv', period)).toBe('report.csv');
  });

  it('falls back to the period for a missing or unsafe name', () => {
    const fallback = 'birge-metrics-2026-10-01-2026-10-28.csv';
    expect(csvFilename(null, period)).toBe(fallback);
    expect(csvFilename('attachment', period)).toBe(fallback);
    expect(csvFilename('attachment; filename="../../etc/passwd"', period)).toBe(fallback);
  });
});
