import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { json, Providers, router } from '@/test-utils';

import { MembersScreen } from './members';

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

const DISTRICTS = [
  {
    id: '01900000-0000-7000-8000-0000000000c1',
    code: 'demo_city',
    districts: [{ id: DISTRICT_A, code: 'demo_district_a' }],
  },
];

const VERIFIED = {
  id: '01900000-0000-7000-8000-0000000000a1',
  displayName: 'Example Aigerim',
  districtId: DISTRICT_A,
  verified: true,
  verifiedAt: '2026-10-20T08:00:00Z',
  suspended: false,
};

const SUSPENDED = {
  id: '01900000-0000-7000-8000-0000000000a2',
  displayName: 'Example Dana',
  verified: true,
  verifiedAt: '2026-09-01T08:00:00Z',
  suspended: true,
  suspendedAt: '2026-10-25T08:00:00Z',
};

const membersRoute = (respond: (req: Request) => Response) => ({ path: '/staff/members', respond });
const mentorRolePath = (memberId: string) => `/staff/members/${memberId}/roles/mentor`;
const districtsRoute = { path: '/reference/districts', respond: () => json(200, DISTRICTS) };

const page = (req: Request) =>
  json(200, { items: new URL(req.url).searchParams.get('status') === 'suspended' ? [SUSPENDED] : [VERIFIED] });

const renderScreen = () =>
  render(
    <Providers>
      <MembersScreen />
    </Providers>,
  );

const calls = () => mockFetch.mock.calls.map((c) => c[0] as Request);

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'mod-1', roles: ['moderator'] });
});

describe('MembersScreen', () => {
  it('renders verified members with their district and date', async () => {
    mockFetch.mockImplementation(router([districtsRoute, membersRoute(page)]));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Members' })).toBeTruthy();
    expect(await screen.findByText('Example Aigerim')).toBeTruthy();
    // The district shows on the row (and as a filter chip above the list).
    expect(await within(screen.getByTestId(`member-${VERIFIED.id}`)).findByText('Demo district A')).toBeTruthy();
    expect(screen.getByText('Verified 2026-10-20')).toBeTruthy();
    expect(calls().find((r) => r.url.includes('/staff/members'))?.url).toContain('status=verified');
    expect(screen.queryByTestId(`lift-${VERIFIED.id}`)).toBeNull();
  });

  it('shows the empty state', async () => {
    mockFetch.mockImplementation(router([districtsRoute, membersRoute(() => json(200, { items: [] }))]));
    await renderScreen();
    expect(await screen.findByText('No verified members yet')).toBeTruthy();
  });

  it('shows a calm state, not an error, while the server answers 501', async () => {
    mockFetch.mockImplementation(
      router([districtsRoute, membersRoute(() => json(501, { title: 'Not implemented', code: 'not_implemented' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
    expect(calls().filter((r) => r.url.includes('/staff/members'))).toHaveLength(1);
  });

  it('shows the error state', async () => {
    mockFetch.mockImplementation(
      router([districtsRoute, membersRoute(() => json(500, { title: 'server error', code: 'internal' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('filters by district', async () => {
    mockFetch.mockImplementation(router([districtsRoute, membersRoute(page)]));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`members-district-${DISTRICT_A}`));
    await waitFor(() => expect(calls().some((r) => r.url.includes(`districtId=${DISTRICT_A}`))).toBe(true));
  });

  it('lifts a suspension after a confirmation', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute(page),
        { method: 'DELETE', path: `/staff/members/${SUSPENDED.id}/suspension`, respond: () => json(204) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId('members-status-suspended'));
    expect(await screen.findByText('Suspended 2026-10-25')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`lift-${SUSPENDED.id}`));
    expect(screen.getByText('She will be able to post and write in chats again.')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('lift-confirm-confirm'));
    expect(await screen.findByText('The suspension is lifted.')).toBeTruthy();
    expect(calls().some((r) => r.method === 'DELETE')).toBe(true);
  });

  it('offers the mentor role actions only on verified, not suspended members', async () => {
    mockFetch.mockImplementation(router([districtsRoute, membersRoute(page)]));
    await renderScreen();
    expect(await screen.findByTestId(`mentor-grant-${VERIFIED.id}`)).toBeTruthy();
    expect(screen.getByTestId(`mentor-revoke-${VERIFIED.id}`)).toBeTruthy();
    expect(screen.getByText(/Whether she already has the mentor role is not shown here/)).toBeTruthy();
    await fireEvent.press(screen.getByTestId('members-status-suspended'));
    expect(await screen.findByText('Suspended 2026-10-25')).toBeTruthy();
    expect(screen.queryByTestId(`mentor-grant-${SUSPENDED.id}`)).toBeNull();
  });

  it('gives the mentor role after a confirmation', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute(page),
        { method: 'PUT', path: mentorRolePath(VERIFIED.id), respond: () => json(204) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`mentor-grant-${VERIFIED.id}`));
    expect(screen.getByText(/Example Aigerim gets the mentor role/)).toBeTruthy();
    await fireEvent.press(screen.getByTestId('mentor-confirm-confirm'));
    expect(await screen.findByText(/She now has the mentor role/)).toBeTruthy();
    expect(calls().some((r) => r.method === 'PUT' && r.url.endsWith(mentorRolePath(VERIFIED.id)))).toBe(true);
  });

  it('takes the mentor role away after a confirmation', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute(page),
        { method: 'DELETE', path: mentorRolePath(VERIFIED.id), respond: () => json(204) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`mentor-revoke-${VERIFIED.id}`));
    expect(screen.getByText(/stops being a mentor in every room she mentors/)).toBeTruthy();
    await fireEvent.press(screen.getByTestId('mentor-confirm-confirm'));
    expect(await screen.findByText('The mentor role is removed.')).toBeTruthy();
    expect(calls().some((r) => r.method === 'DELETE' && r.url.endsWith(mentorRolePath(VERIFIED.id)))).toBe(true);
  });

  it('explains a 409 when she is not verified or is suspended', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute(page),
        {
          method: 'PUT',
          path: mentorRolePath(VERIFIED.id),
          respond: () => json(409, { title: 'conflict', code: 'conflict' }),
        },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`mentor-grant-${VERIFIED.id}`));
    await fireEvent.press(screen.getByTestId('mentor-confirm-confirm'));
    expect(await screen.findByText(/only to a verified member who is not suspended/)).toBeTruthy();
  });

  it('says the mentor role is not available yet while the server answers 501', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute(page),
        {
          method: 'DELETE',
          path: mentorRolePath(VERIFIED.id),
          respond: () => json(501, { title: 'Not implemented', code: 'not_implemented' }),
        },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`mentor-revoke-${VERIFIED.id}`));
    await fireEvent.press(screen.getByTestId('mentor-confirm-confirm'));
    expect(await screen.findByText(/cannot be changed on this server yet/)).toBeTruthy();
    expect(screen.queryByText('The request did not go through. Try again.')).toBeNull();
  });

  it('explains that only an admin can lift a suspension on 403', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute(page),
        {
          method: 'DELETE',
          path: `/staff/members/${SUSPENDED.id}/suspension`,
          respond: () => json(403, { title: 'forbidden', code: 'forbidden' }),
        },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId('members-status-suspended'));
    await fireEvent.press(await screen.findByTestId(`lift-${SUSPENDED.id}`));
    await fireEvent.press(screen.getByTestId('lift-confirm-confirm'));
    expect(await screen.findByText(/Only an admin can lift a suspension/)).toBeTruthy();
  });
});
