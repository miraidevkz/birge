import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { presetPeriod, todayUtc } from '@/lib/metrics';
import { json, Providers, router as fetchRouter } from '@/test-utils';

import { MetricsScreen } from './metrics';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-web-browser', () => ({
  maybeCompleteAuthSession: jest.fn(),
}));
jest.mock('expo-auth-session', () => ({
  AuthRequest: jest.fn(),
  ResponseType: { Code: 'code' },
  exchangeCodeAsync: jest.fn(),
  fetchDiscoveryAsync: jest.fn(),
  makeRedirectUri: jest.fn(() => 'https://admin.example.invalid/'),
}));
jest.mock('@/lib/save-file', () => ({ saveTextFile: jest.fn() }));

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  const { getAccessToken } = require('@/lib/auth');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'web',
      session: getAccessToken,
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000004',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
  };
});

const saveTextFile = () => (require('@/lib/save-file') as { saveTextFile: jest.Mock }).saveTextFile;

const HIDDEN = { suppressed: true };
const shown = (value: number, members = 12) => ({ suppressed: false, members, value });

// Synthetic aggregates only. k = 5; some cells are suppressed as the server sends them.
const METRICS = {
  from: '2026-10-01',
  to: '2026-10-28',
  k: 5,
  stress: {
    baseline: { mean: shown(8.25, 9), median: shown(8, 9) },
    week8: { mean: HIDDEN, median: HIDDEN },
  },
  isolation: {
    before: { mean: HIDDEN, median: HIDDEN },
    after: { mean: HIDDEN, median: HIDDEN },
  },
  activity: {
    weekly: [
      { bucket: '2026-09-28', cell: shown(14, 14) },
      { bucket: '2026-10-05', cell: HIDDEN },
    ],
    monthly: [{ bucket: '2026-10-01', cell: shown(20, 20) }],
    retention4w: shown(0.625, 8),
    retention8w: HIDDEN,
  },
  tea: { requests: shown(7, 7), pairs: HIDDEN, meetings: HIDDEN },
  sos: { raised: shown(6, 5), closedShare: shown(0.5, 5), firstResponseMedianMinutes: shown(4.4, 5) },
  escalations: { opened: shown(9, 6), takenWithin15mShare: shown(0.889, 6) },
  rights: { answeredFromKnowledge: HIDDEN, helpedShare: HIDDEN },
};

const metricsRoute = (respond: (req: Request) => Response) => ({ path: '/staff/metrics', respond });

const metricsCalls = () =>
  mockFetch.mock.calls.map((c) => new URL((c[0] as Request).url)).filter((u) => u.pathname.endsWith('/staff/metrics'));

const csvCalls = () =>
  mockFetch.mock.calls.map((c) => c[0] as Request).filter((r) => new URL(r.url).pathname.endsWith('/metrics.csv'));

const renderScreen = () =>
  render(
    <Providers>
      <MetricsScreen />
    </Providers>,
  );

const row = (id: string) => within(screen.getByTestId(`metrics-row-${id}`));

beforeEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'admin-1', roles: ['admin'] });
});

describe('MetricsScreen', () => {
  it('reads the last 4 weeks first and renders every section with units', async () => {
    mockFetch.mockImplementation(fetchRouter([metricsRoute(() => json(200, METRICS))]));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Pilot metrics' })).toBeTruthy();
    await screen.findByTestId('metrics-activity');

    const expected = presetPeriod('last4w', todayUtc());
    expect(metricsCalls()[0]?.searchParams.get('from')).toBe(expected?.from);
    expect(metricsCalls()[0]?.searchParams.get('to')).toBe(expected?.to);

    for (const id of ['activity', 'retention', 'tea', 'sos', 'escalations', 'rights', 'stress', 'isolation']) {
      expect(screen.getByTestId(`metrics-${id}`)).toBeTruthy();
    }
    expect(screen.getByText('Numbers built from fewer than 5 members are hidden to protect privacy.')).toBeTruthy();
    // A short line on what each section counts.
    expect(screen.getByText(/the share taken within 15 minutes/)).toBeTruthy();

    expect(row('retention4w').getByText('63%')).toBeTruthy();
    expect(row('retention4w').getByText('From 8 members')).toBeTruthy();
    expect(row('tea-requests').getByText('7')).toBeTruthy();
    expect(row('sos-raised').getByText('6')).toBeTruthy();
    expect(row('sos-closed').getByText('50%')).toBeTruthy();
    expect(row('sos-first').getByText('4 min')).toBeTruthy();
    expect(row('esc-opened').getByText('9')).toBeTruthy();
    expect(row('esc-taken').getByText('89%')).toBeTruthy();
    expect(row('stress-baseline-members').getByText('9')).toBeTruthy();
    expect(row('stress-baseline-mean').getByText('8.3')).toBeTruthy();
    expect(row('stress-baseline-median').getByText('8')).toBeTruthy();

    // Series: weekly and monthly bars.
    expect(screen.getByLabelText('Active members per week, Week of 2026-09-28: 14')).toBeTruthy();
    expect(screen.getByLabelText('Active members per month, 2026-10: 20')).toBeTruthy();
  });

  it('shows a suppressed cell as the privacy marker, never as 0 or an empty number', async () => {
    mockFetch.mockImplementation(fetchRouter([metricsRoute(() => json(200, METRICS))]));
    await renderScreen();
    await screen.findByTestId('metrics-activity');

    const marker = 'Fewer than 5 members — hidden to protect privacy';
    for (const id of [
      'retention8w',
      'tea-pairs',
      'tea-meetings',
      'rights-answered',
      'rights-helped',
      'stress-week8',
      'isolation-before',
      'isolation-after',
    ]) {
      const r = row(id);
      expect(r.getByText(marker)).toBeTruthy();
      expect(r.queryByText('0')).toBeNull();
      expect(r.queryByText('0%')).toBeNull();
    }
    // A hidden week has no bar, only the short marker.
    expect(screen.getByLabelText('Active members per week, Week of 2026-10-05: Fewer than 5 — hidden')).toBeTruthy();
    expect(screen.queryByTestId('metrics-weekly-2026-10-05-bar', { includeHiddenElements: true })).toBeNull();
    expect(screen.getByTestId('metrics-weekly-2026-09-28-bar', { includeHiddenElements: true })).toBeTruthy();
  });

  it('changes the period with a preset', async () => {
    mockFetch.mockImplementation(fetchRouter([metricsRoute(() => json(200, METRICS))]));
    await renderScreen();
    await screen.findByTestId('metrics-activity');
    await fireEvent.press(screen.getByTestId('metrics-preset-ytd'));
    const today = todayUtc();
    await waitFor(() =>
      expect(metricsCalls().some((u) => u.searchParams.get('from') === `${today.slice(0, 4)}-01-01`)).toBe(true),
    );
  });

  it('sends a custom period up to 12 months and holds back a longer one', async () => {
    mockFetch.mockImplementation(fetchRouter([metricsRoute(() => json(200, METRICS))]));
    await renderScreen();
    await screen.findByTestId('metrics-activity');
    await fireEvent.press(screen.getByTestId('metrics-preset-custom'));
    await fireEvent.changeText(screen.getByTestId('metrics-from'), '2025-10-28');
    await fireEvent.changeText(screen.getByTestId('metrics-to'), '2026-10-29');
    expect(
      await screen.findByText('A period can be at most 12 months long. Choose a later start or an earlier end.'),
    ).toBeTruthy();
    // Matched with `from` too: the default preset ends today, which may be 2026-10-29.
    expect(
      metricsCalls().some(
        (u) => u.searchParams.get('from') === '2025-10-28' && u.searchParams.get('to') === '2026-10-29',
      ),
    ).toBe(false);
    expect(screen.getByTestId('metrics-download')).toBeDisabled();

    await fireEvent.changeText(screen.getByTestId('metrics-to'), '2026-10-28');
    await waitFor(() =>
      expect(
        metricsCalls().some(
          (u) => u.searchParams.get('from') === '2025-10-28' && u.searchParams.get('to') === '2026-10-28',
        ),
      ).toBe(true),
    );
  });

  it('flags a reversed custom period and does not send it', async () => {
    mockFetch.mockImplementation(fetchRouter([metricsRoute(() => json(200, METRICS))]));
    await renderScreen();
    await screen.findByTestId('metrics-activity');
    await fireEvent.press(screen.getByTestId('metrics-preset-custom'));
    await fireEvent.changeText(screen.getByTestId('metrics-from'), '2026-10-10');
    await fireEvent.changeText(screen.getByTestId('metrics-to'), '2026-10-01');
    expect(await screen.findByText('The end date is before the start date.')).toBeTruthy();
    expect(metricsCalls().some((u) => u.searchParams.get('from') === '2026-10-10')).toBe(false);
  });

  it('explains the period rule on 400 invalid_period', async () => {
    mockFetch.mockImplementation(
      fetchRouter([metricsRoute(() => json(400, { title: 'The period is invalid', code: 'invalid_period' }))]),
    );
    await renderScreen();
    expect(await screen.findByTestId('metrics-invalid-period')).toBeTruthy();
    expect(screen.getByText(/The server cannot show this period/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
  });

  it('says only an admin can see metrics on 403', async () => {
    mockFetch.mockImplementation(
      fetchRouter([metricsRoute(() => json(403, { title: 'forbidden', code: 'forbidden' }))]),
    );
    await renderScreen();
    expect(await screen.findByText('Only an admin can see metrics')).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
    expect(screen.queryByTestId('metrics-download')).toBeNull();
  });

  it('shows a calm state while the server answers 501', async () => {
    mockFetch.mockImplementation(
      fetchRouter([metricsRoute(() => json(501, { title: 'Not implemented', code: 'not_implemented' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
  });

  it('shows a calm state while the route is not mounted (404 without not_found)', async () => {
    mockFetch.mockImplementation(
      async () => new Response('404 page not found', { status: 404, headers: { 'Content-Type': 'text/plain' } }),
    );
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(metricsCalls()).toHaveLength(1);
  });

  it('shows the error state with retry', async () => {
    mockFetch.mockImplementation(fetchRouter([metricsRoute(() => json(500, { title: 'error', code: 'internal' }))]));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('downloads the CSV with the session and saves it under the server file name', async () => {
    const csv = 'metric,bucket,members,value,suppressed\nsos.raised,,5,6,false\n';
    mockFetch.mockImplementation(
      fetchRouter([
        metricsRoute(() => json(200, METRICS)),
        {
          path: '/staff/metrics.csv',
          respond: () =>
            new Response(csv, {
              status: 200,
              headers: {
                'Content-Type': 'text/csv; charset=utf-8',
                'Content-Disposition': 'attachment; filename="birge-metrics-2026-10-01-2026-10-28.csv"',
              },
            }),
        },
      ]),
    );
    await renderScreen();
    await screen.findByTestId('metrics-activity');
    await fireEvent.press(screen.getByTestId('metrics-download'));
    await waitFor(() => expect(saveTextFile()).toHaveBeenCalledTimes(1));
    expect(saveTextFile()).toHaveBeenCalledWith(
      csv,
      'birge-metrics-2026-10-01-2026-10-28.csv',
      'text/csv;charset=utf-8',
    );
    const req = csvCalls()[0];
    expect(req?.headers.get('Authorization')).toBe('Bearer staff-token');
    const expected = presetPeriod('last4w', todayUtc());
    expect(new URL(req?.url ?? 'https://x.invalid').searchParams.get('from')).toBe(expected?.from);
  });

  it('says so when the download fails', async () => {
    mockFetch.mockImplementation(
      fetchRouter([
        metricsRoute(() => json(200, METRICS)),
        { path: '/staff/metrics.csv', respond: () => json(500, { title: 'error', code: 'internal' }) },
      ]),
    );
    await renderScreen();
    await screen.findByTestId('metrics-activity');
    await fireEvent.press(screen.getByTestId('metrics-download'));
    expect(await screen.findByTestId('metrics-download-failed')).toBeTruthy();
    expect(saveTextFile()).not.toHaveBeenCalled();
  });
});
