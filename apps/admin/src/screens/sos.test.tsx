import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { json, Providers, router, type Route } from '@/test-utils';

import { SosQueueScreen } from './sos';

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
jest.mock('expo-router', () => ({
  // Focus once on mount, as the tab does when it is opened.
  useFocusEffect: (cb: () => void | (() => void)) => require('react').useEffect(cb, [cb]),
}));

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

const DISTRICT_A = '01900000-0000-7000-8000-0000000000d1';
const STAFF_ID = '01900000-0000-7000-8000-0000000000f1';

const DISTRICTS = [
  {
    id: '01900000-0000-7000-8000-0000000000c1',
    code: 'demo_city',
    districts: [{ id: DISTRICT_A, code: 'demo_district_a' }],
  },
];

const ITEM_SOS = {
  id: '01900000-0000-7000-8000-0000000000s1',
  memberId: '01900000-0000-7000-8000-0000000000a1',
  displayName: 'Example Aigerim',
  districtId: DISTRICT_A,
  type: 'item_or_medicine',
  text: 'Example: need a thermometer tonight',
  status: 'open',
  sharedToDistrict: true,
  createdAt: '2026-10-28T08:00:00Z',
  closesAt: '2026-10-29T08:00:00Z',
  responseCount: 2,
};

const CHILDCARE_SOS = {
  id: '01900000-0000-7000-8000-0000000000s2',
  memberId: '01900000-0000-7000-8000-0000000000a2',
  displayName: 'Example Dana',
  type: 'childcare',
  text: 'Example: childcare for two hours',
  status: 'open',
  sharedToDistrict: false,
  createdAt: '2026-10-28T09:00:00Z',
  closesAt: '2026-10-29T09:00:00Z',
  responseCount: 0,
};

const CLOSED_SOS = {
  ...ITEM_SOS,
  id: '01900000-0000-7000-8000-0000000000s3',
  text: 'Example: a closed request',
  status: 'closed',
  closedAt: '2026-10-28T12:00:00Z',
};

const districtsRoute: Route = { path: '/reference/districts', respond: () => json(200, DISTRICTS) };

const queueRoute = (respond: Route['respond']): Route => ({ path: '/staff/sos', respond });

const renderScreen = () =>
  render(
    <Providers>
      <SosQueueScreen />
    </Providers>,
  );

const calls = () => mockFetch.mock.calls.map((c) => c[0] as Request);
const posts = () => calls().filter((r) => r.method === 'POST');

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'mod-1', roles: ['moderator'] });
});

describe('SosQueueScreen', () => {
  it('renders a row with labels, people, times and counts', async () => {
    mockFetch.mockImplementation(router([districtsRoute, queueRoute(() => json(200, { items: [ITEM_SOS] }))]));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'SOS requests' })).toBeTruthy();
    expect(await screen.findByText('Example: need a thermometer tonight')).toBeTruthy();
    expect(screen.getByText('Urgently needs an item or medicine')).toBeTruthy();
    expect(screen.getByText('From: Example Aigerim')).toBeTruthy();
    expect(await screen.findByText('District: Demo district A')).toBeTruthy();
    expect(screen.getByText('Raised 2026-10-28 08:00 UTC')).toBeTruthy();
    expect(screen.getByText('Closes automatically 2026-10-29 08:00 UTC')).toBeTruthy();
    expect(screen.getByText('Offers of help: 2')).toBeTruthy();
    expect(screen.getByText('Shared to her district feed')).toBeTruthy();
    expect(screen.getByText('Not marked as seen yet')).toBeTruthy();
    expect(screen.getByTestId(`seen-${ITEM_SOS.id}`)).toBeTruthy();
    expect(screen.getByTestId(`close-${ITEM_SOS.id}`)).toBeTruthy();
    // Open is the default segment.
    const list = calls().find((r) => r.url.includes('/staff/sos'));
    expect(list?.url).toContain('status=open');
  });

  it('marks a childcare SOS as going only to trusted contacts and moderators', async () => {
    mockFetch.mockImplementation(router([districtsRoute, queueRoute(() => json(200, { items: [CHILDCARE_SOS] }))]));
    await renderScreen();
    expect(await screen.findByText('No one to leave the child with')).toBeTruthy();
    expect(screen.getByTestId(`sos-childcare-${CHILDCARE_SOS.id}`)).toBeTruthy();
    expect(
      screen.getByText('Goes only to her trusted contacts and the moderators. It is never shared to the district.'),
    ).toBeTruthy();
    expect(screen.queryByText('Not shared to the district')).toBeNull();
    expect(screen.getByText('No district in her profile')).toBeTruthy();
  });

  it('shows an unknown type through the generic label, never the raw code', async () => {
    const future = { ...ITEM_SOS, type: 'future_type' };
    mockFetch.mockImplementation(router([districtsRoute, queueRoute(() => json(200, { items: [future] }))]));
    await renderScreen();
    expect(await screen.findByText('Another kind of request')).toBeTruthy();
    expect(screen.queryByText(/future_type/)).toBeNull();
  });

  it('switches to the closed segment with status=closed', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        queueRoute((req) =>
          new URL(req.url).searchParams.get('status') === 'closed'
            ? json(200, { items: [CLOSED_SOS] })
            : json(200, { items: [ITEM_SOS] }),
        ),
      ]),
    );
    await renderScreen();
    await screen.findByText('Example: need a thermometer tonight');
    await fireEvent.press(screen.getByTestId('sos-status-closed'));
    expect(await screen.findByText('Example: a closed request')).toBeTruthy();
    expect(screen.getByText('Closed 2026-10-28 12:00 UTC')).toBeTruthy();
    expect(screen.queryByTestId(`seen-${CLOSED_SOS.id}`)).toBeNull();
    expect(screen.queryByTestId(`close-${CLOSED_SOS.id}`)).toBeNull();
  });

  it('loads the next page with the cursor', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        queueRoute((req) =>
          req.url.includes('cursor=page2')
            ? json(200, { items: [CHILDCARE_SOS] })
            : json(200, { items: [ITEM_SOS], nextCursor: 'page2' }),
        ),
      ]),
    );
    await renderScreen();
    expect(await screen.findByText('Example: need a thermometer tonight')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Show more' }));
    expect(await screen.findByText('Example: childcare for two hours')).toBeTruthy();
    expect(screen.getByText('Example: need a thermometer tonight')).toBeTruthy();
  });

  it('marks an SOS seen and shows who saw it first from the response', async () => {
    const seenRow = { ...ITEM_SOS, seenAt: '2026-10-28T08:05:00Z', seenBy: STAFF_ID };
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        queueRoute(() => json(200, { items: [ITEM_SOS] })),
        { method: 'POST', path: `/staff/sos/${ITEM_SOS.id}/seen`, respond: () => json(200, seenRow) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`seen-${ITEM_SOS.id}`));
    // A moderator cannot read the staff directory: the short staff ID stands in.
    expect(await screen.findByText('Seen first by 01900000, 2026-10-28 08:05 UTC')).toBeTruthy();
    expect(screen.queryByTestId(`seen-${ITEM_SOS.id}`)).toBeNull();
    expect(posts()).toHaveLength(1);
    expect(posts()[0]!.url).toContain(`/staff/sos/${ITEM_SOS.id}/seen`);
    // The row came from the response; no directory read for a moderator.
    expect(calls().some((r) => r.url.includes('/staff/accounts'))).toBe(false);
  });

  it('names the first viewer from the staff directory for an admin', async () => {
    setAccessToken('staff-token', { subject: 'admin-1', roles: ['admin'] });
    const seenRow = { ...ITEM_SOS, seenAt: '2026-10-28T08:05:00Z', seenBy: STAFF_ID };
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        queueRoute(() => json(200, { items: [seenRow] })),
        {
          path: '/staff/accounts',
          respond: () =>
            json(200, {
              items: [
                {
                  id: STAFF_ID,
                  displayName: 'Example Moderator',
                  roles: ['moderator'],
                  firstSeenAt: '2026-10-01T08:00:00Z',
                  lastSeenAt: '2026-10-28T08:00:00Z',
                },
              ],
            }),
        },
      ]),
    );
    await renderScreen();
    expect(await screen.findByText('Seen first by Example Moderator, 2026-10-28 08:05 UTC')).toBeTruthy();
  });

  it('closes an SOS only after a confirmation and shows the stored row', async () => {
    const closedRow = { ...ITEM_SOS, status: 'closed', closedAt: '2026-10-28T10:00:00Z' };
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        queueRoute(() => json(200, { items: [ITEM_SOS] })),
        { method: 'POST', path: `/staff/sos/${ITEM_SOS.id}/close`, respond: () => json(200, closedRow) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`close-${ITEM_SOS.id}`));
    expect(screen.getByText('Close this SOS?')).toBeTruthy();
    expect(posts()).toHaveLength(0);
    await fireEvent.press(screen.getByTestId('sos-close-confirm-confirm'));
    expect(await screen.findByText('Closed 2026-10-28 10:00 UTC')).toBeTruthy();
    expect(posts()).toHaveLength(1);
    expect(posts()[0]!.url).toContain(`/staff/sos/${ITEM_SOS.id}/close`);
    expect(screen.queryByTestId(`close-${ITEM_SOS.id}`)).toBeNull();
    expect(screen.queryByText('Closes automatically 2026-10-29 08:00 UTC')).toBeNull();
  });

  it('says that only moderators and admins see SOS on 403', async () => {
    mockFetch.mockImplementation(
      router([districtsRoute, queueRoute(() => json(403, { title: 'forbidden', code: 'forbidden' }))]),
    );
    await renderScreen();
    expect(await screen.findByText('Only moderators and admins see SOS')).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
    expect(screen.queryByTestId('sos-status-open')).toBeNull();
  });

  it('shows the not-available state on 501', async () => {
    mockFetch.mockImplementation(
      router([districtsRoute, queueRoute(() => json(501, { title: 'Not implemented', code: 'not_implemented' }))]),
    );
    await renderScreen();
    expect(await screen.findByText('Not on this server yet')).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
  });

  it('shows the not-available state when the route is not mounted (404 without not_found)', async () => {
    mockFetch.mockImplementation(router([districtsRoute, queueRoute(() => json(404))]));
    await renderScreen();
    expect(await screen.findByText('Not on this server yet')).toBeTruthy();
  });

  it('offers a retry on a network error', async () => {
    let fail = true;
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        queueRoute(() => {
          if (fail) throw new TypeError('Network request failed');
          return json(200, { items: [ITEM_SOS] });
        }),
      ]),
    );
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
    fail = false;
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Example: need a thermometer tonight')).toBeTruthy();
  });

  it('shows a banner when an action fails', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        queueRoute(() => json(200, { items: [ITEM_SOS] })),
        {
          method: 'POST',
          path: `/staff/sos/${ITEM_SOS.id}/seen`,
          respond: () => json(403, { title: 'forbidden', code: 'forbidden' }),
        },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`seen-${ITEM_SOS.id}`));
    expect(
      await screen.findByText('SOS requests are open to the moderators and admins of this deployment.'),
    ).toBeTruthy();
  });

  it('never logs the text or the name', async () => {
    const spies = (['log', 'info', 'warn', 'error', 'debug'] as const).map((m) =>
      jest.spyOn(console, m).mockImplementation(() => undefined),
    );
    mockFetch.mockImplementation(router([districtsRoute, queueRoute(() => json(200, { items: [ITEM_SOS] }))]));
    await renderScreen();
    await screen.findByText('Example: need a thermometer tonight');
    const logged = spies.flatMap((s) => s.mock.calls.flat().map(String)).join('\n');
    expect(logged).not.toContain('thermometer');
    expect(logged).not.toContain('Aigerim');
    spies.forEach((s) => s.mockRestore());
  });
});
