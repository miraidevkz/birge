import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { json, Providers, router as fetchRouter } from '@/test-utils';

import { AuditScreen } from './audit';

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
  router: { push: jest.fn(), navigate: jest.fn(), setParams: jest.fn(), back: jest.fn(), dismissTo: jest.fn() },
  useLocalSearchParams: jest.fn(() => ({})),
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

type RouterMock = {
  router: { push: jest.Mock; navigate: jest.Mock; setParams: jest.Mock };
  useLocalSearchParams: jest.Mock;
};
const expoRouter = () => require('expo-router') as RouterMock;

const STAFF_A = '01900000-0000-7000-8000-0000000000e1';
const STAFF_B = '01900000-0000-7000-8000-0000000000e2';

const KNOWN = {
  id: '01900000-0000-7000-8000-0000000000f1',
  occurredAt: '2026-10-27T09:15:00Z',
  actorId: STAFF_A,
  actorRole: 'psychologist',
  actorName: 'Example Psychologist',
  action: 'escalation.take',
  objectType: 'safety.escalation',
  objectId: 'abcdef12-0000-7000-8000-000000000001',
};

const UNKNOWN = {
  id: '01900000-0000-7000-8000-0000000000f2',
  occurredAt: '2026-10-26T18:00:00Z',
  actorId: STAFF_B,
  actorRole: 'moderator',
  action: 'future.thing',
  objectType: 'future.object',
  reason: 'mystery_code',
};

const ACCOUNTS = {
  items: [
    {
      id: STAFF_A,
      displayName: 'Example Psychologist',
      roles: ['psychologist'],
      firstSeenAt: '2026-09-01T08:00:00Z',
      lastSeenAt: '2026-10-27T09:00:00Z',
    },
  ],
};

const accountsRoute = { path: '/staff/accounts', respond: () => json(200, ACCOUNTS) };
const auditRoute = (respond: (req: Request) => Response) => ({ path: '/staff/audit', respond });

function pages(req: Request): Response {
  if (new URL(req.url).searchParams.get('cursor') === 'page-2') return json(200, { items: [UNKNOWN] });
  return json(200, { items: [KNOWN], nextCursor: 'page-2' });
}

const renderScreen = () =>
  render(
    <Providers>
      <AuditScreen />
    </Providers>,
  );

const auditCalls = () =>
  mockFetch.mock.calls.map((c) => new URL((c[0] as Request).url)).filter((u) => u.pathname.endsWith('/staff/audit'));

/** sent reports whether some audit read carried the query parameter with this value. */
const sent = (key: string, value: string) => auditCalls().some((u) => u.searchParams.get(key) === value);

beforeEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
  expoRouter().useLocalSearchParams.mockReturnValue({});
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'admin-1', roles: ['admin'] });
});

describe('AuditScreen', () => {
  it('renders known codes as labels and unknown codes with a generic label and the code', async () => {
    mockFetch.mockImplementation(
      fetchRouter([accountsRoute, auditRoute(() => json(200, { items: [KNOWN, UNKNOWN] }))]),
    );
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Audit log' })).toBeTruthy();

    const known = within(await screen.findByTestId(`audit-${KNOWN.id}`));
    expect(known.getByText('Took an escalation')).toBeTruthy();
    expect(known.getByText('Escalation · abcdef12')).toBeTruthy();
    expect(known.getByText('Role: Psychologist')).toBeTruthy();
    expect(known.getByText('2026-10-27 09:15 UTC')).toBeTruthy();
    expect(known.getByRole('button', { name: 'Example Psychologist' })).toBeTruthy();
    expect(known.getByRole('button', { name: 'Open in Escalations' })).toBeTruthy();

    const unknown = within(screen.getByTestId(`audit-${UNKNOWN.id}`));
    expect(unknown.getByText('Other action')).toBeTruthy();
    expect(unknown.getByText('future.thing')).toBeTruthy();
    expect(unknown.getByText('Other object')).toBeTruthy();
    expect(unknown.getByText('future.object')).toBeTruthy();
    expect(unknown.getByText('Reason: Other reason')).toBeTruthy();
    expect(unknown.getByText('mystery_code')).toBeTruthy();
    // No name from the IdP: a short ID; no screen for that object: no link.
    expect(unknown.getByRole('button', { name: STAFF_B.slice(0, 8) })).toBeTruthy();
    expect(screen.queryByTestId(`audit-object-${UNKNOWN.id}`)).toBeNull();
  });

  it('names the external contact plan view and gives an unknown role a generic label', async () => {
    // The contract's AuditActorRole does not list these roles yet; the server writes them.
    const external = {
      id: '01900000-0000-7000-8000-0000000000f3',
      occurredAt: '2026-10-28T07:00:00Z',
      actorId: STAFF_B,
      actorRole: 'external_contact',
      action: 'emergency_plan.view_external',
      objectType: 'emergency.child_card',
      objectId: 'abcdef12-0000-7000-8000-000000000003',
    };
    const futureRole = { ...KNOWN, id: '01900000-0000-7000-8000-0000000000f4', actorRole: 'future_role' };
    mockFetch.mockImplementation(
      fetchRouter([accountsRoute, auditRoute(() => json(200, { items: [external, futureRole] }))]),
    );
    await renderScreen();

    const ext = within(await screen.findByTestId(`audit-${external.id}`));
    expect(ext.getByText('Opened a sent emergency plan by its link')).toBeTruthy();
    expect(ext.getByText('Child card · abcdef12')).toBeTruthy();
    expect(ext.getByText('Role: External trusted contact')).toBeTruthy();
    expect(ext.queryByText('external_contact')).toBeNull();

    const other = within(screen.getByTestId(`audit-${futureRole.id}`));
    expect(other.getByText('Role: Other role')).toBeTruthy();
    expect(other.getByText('future_role')).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute, auditRoute(() => json(200, { items: [] }))]));
    await renderScreen();
    expect(await screen.findByText('No entries')).toBeTruthy();
  });

  it('shows a calm state while the route is not mounted (404 without not_found)', async () => {
    // The server's router answers before any Birge handler: plain text, no problem code.
    mockFetch.mockImplementation(
      async () => new Response('404 page not found', { status: 404, headers: { 'Content-Type': 'text/plain' } }),
    );
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
    expect(auditCalls()).toHaveLength(1);
  });

  it('shows a calm state while the server answers 501', async () => {
    mockFetch.mockImplementation(
      fetchRouter([accountsRoute, auditRoute(() => json(501, { title: 'Not implemented', code: 'not_implemented' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
  });

  it('says only an admin can see the log on 403', async () => {
    mockFetch.mockImplementation(
      fetchRouter([
        { path: '/staff/accounts', respond: () => json(403, { title: 'forbidden', code: 'forbidden' }) },
        auditRoute(() => json(403, { title: 'forbidden', code: 'forbidden' })),
      ]),
    );
    await renderScreen();
    expect(await screen.findByText('Only an admin can see this')).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
    expect(screen.queryByTestId('audit-action')).toBeNull();
  });

  it('shows the error state', async () => {
    mockFetch.mockImplementation(
      fetchRouter([accountsRoute, auditRoute(() => json(500, { title: 'server error', code: 'internal' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('filters by action, object type and dates', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute, auditRoute(() => json(200, { items: [KNOWN] }))]));
    await renderScreen();
    await screen.findByTestId(`audit-${KNOWN.id}`);
    await fireEvent.press(screen.getByTestId('audit-action-escalation.take'));
    await waitFor(() => expect(sent('action', 'escalation.take')).toBe(true));
    await fireEvent.press(screen.getByTestId('audit-object-safety.escalation'));
    await waitFor(() => expect(sent('objectType', 'safety.escalation')).toBe(true));
    await fireEvent.changeText(screen.getByTestId('audit-from'), '2026-10-01');
    await fireEvent.changeText(screen.getByTestId('audit-to'), '2026-10-27');
    await waitFor(() => expect(sent('from', '2026-10-01T00:00:00Z')).toBe(true));
    expect(sent('to', '2026-10-27T23:59:59Z')).toBe(true);
  });

  it('flags an impossible date and does not send it', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute, auditRoute(() => json(200, { items: [KNOWN] }))]));
    await renderScreen();
    await screen.findByTestId(`audit-${KNOWN.id}`);
    await fireEvent.changeText(screen.getByTestId('audit-from'), '2026-02-30');
    expect(await screen.findByText('Enter a real date as YYYY-MM-DD.')).toBeTruthy();
    expect(auditCalls().some((u) => u.searchParams.has('from'))).toBe(false);
  });

  it('starts with the person filter from the route (from a staff card) and changes it', async () => {
    expoRouter().useLocalSearchParams.mockReturnValue({ actorId: STAFF_A });
    mockFetch.mockImplementation(fetchRouter([accountsRoute, auditRoute(() => json(200, { items: [KNOWN] }))]));
    await renderScreen();
    await screen.findByTestId(`audit-${KNOWN.id}`);
    expect(auditCalls()[0]?.searchParams.get('actorId')).toBe(STAFF_A);
    expect(await screen.findByTestId(`audit-person-${STAFF_A}`)).toBeTruthy();

    await fireEvent.press(screen.getByTestId('audit-person-all'));
    await waitFor(() => expect(auditCalls().some((u) => !u.searchParams.has('actorId'))).toBe(true));
    expect(expoRouter().router.setParams).toHaveBeenCalledWith({ actorId: undefined });
  });

  it('opens the staff card from a row, the staff list and the object screen', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute, auditRoute(() => json(200, { items: [KNOWN] }))]));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`audit-actor-${KNOWN.id}`));
    expect(expoRouter().router.push).toHaveBeenCalledWith({
      pathname: '/audit/staff/[staffId]',
      params: { staffId: STAFF_A },
    });
    await fireEvent.press(screen.getByTestId(`audit-object-${KNOWN.id}`));
    expect(expoRouter().router.navigate).toHaveBeenCalledWith('/');
    await fireEvent.press(screen.getByTestId('audit-open-staff'));
    expect(expoRouter().router.push).toHaveBeenCalledWith('/audit/staff');
  });

  it('loads the next page with the cursor', async () => {
    mockFetch.mockImplementation(fetchRouter([accountsRoute, auditRoute(pages)]));
    await renderScreen();
    await screen.findByTestId(`audit-${KNOWN.id}`);
    await fireEvent.press(screen.getByRole('button', { name: 'Show more' }));
    expect(await screen.findByTestId(`audit-${UNKNOWN.id}`)).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Show more' })).toBeNull();
  });
});
