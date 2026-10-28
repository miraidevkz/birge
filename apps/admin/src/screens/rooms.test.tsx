import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { json, Providers, router } from '@/test-utils';

import { RoomsScreen } from './rooms';

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

const ROOM = {
  id: '01900000-0000-7000-8000-0000000000r1',
  kind: 'district',
  title: 'chat.room.district.demo_district_a',
  memberCount: 12,
  mentors: [{ memberId: '01900000-0000-7000-8000-0000000000a9', displayName: 'Example Saule' }],
};

const MEMBER = {
  id: '01900000-0000-7000-8000-0000000000a1',
  displayName: 'Example Aigerim',
  verified: true,
  suspended: false,
};

const roomsRoute = (respond: () => Response) => ({ path: '/staff/chat/rooms', respond });
const membersRoute = { path: '/staff/members', respond: () => json(200, { items: [MEMBER] }) };
const districtsRoute = { path: '/reference/districts', respond: () => json(200, []) };
const mentorPath = (memberId: string) => `/staff/chat/rooms/${ROOM.id}/mentors/${memberId}`;

const renderScreen = () =>
  render(
    <Providers>
      <RoomsScreen />
    </Providers>,
  );

const calls = () => mockFetch.mock.calls.map((c) => c[0] as Request);

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'mod-1', roles: ['moderator'] });
});

describe('RoomsScreen', () => {
  it('renders rooms with the localized title, member count and mentors', async () => {
    mockFetch.mockImplementation(router([districtsRoute, roomsRoute(() => json(200, { items: [ROOM] }))]));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Rooms' })).toBeTruthy();
    expect(await screen.findByText('Mothers of Demo district A')).toBeTruthy();
    expect(screen.getByText('Members: 12')).toBeTruthy();
    expect(screen.getByText('Example Saule')).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockImplementation(router([districtsRoute, roomsRoute(() => json(200, { items: [] }))]));
    await renderScreen();
    expect(await screen.findByText('No group rooms yet')).toBeTruthy();
  });

  it('shows a calm state, not an error, while the server answers 501', async () => {
    mockFetch.mockImplementation(
      router([districtsRoute, roomsRoute(() => json(501, { title: 'Not implemented', code: 'not_implemented' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
    expect(screen.queryByText(/Something went wrong/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Try again' })).toBeNull();
  });

  it('shows the error state', async () => {
    mockFetch.mockImplementation(
      router([districtsRoute, roomsRoute(() => json(500, { title: 'server error', code: 'internal' }))]),
    );
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('assigns a mentor picked from verified members', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute,
        roomsRoute(() => json(200, { items: [ROOM] })),
        { method: 'PUT', path: mentorPath(MEMBER.id), respond: () => json(204) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`add-mentor-${ROOM.id}`));
    expect(await screen.findByRole('header', { name: 'Choose a mentor' })).toBeTruthy();
    await fireEvent.press(await screen.findByTestId(`pick-${MEMBER.id}`));
    expect(await screen.findByText('Mentor assigned.')).toBeTruthy();
    expect(calls().find((r) => r.method === 'PUT')?.url).toContain(mentorPath(MEMBER.id));
    expect(calls().find((r) => r.url.includes('/staff/members'))?.url).toContain('status=verified');
  });

  it('explains a 409 when she cannot be a mentor', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        membersRoute,
        roomsRoute(() => json(200, { items: [ROOM] })),
        {
          method: 'PUT',
          path: mentorPath(MEMBER.id),
          respond: () => json(409, { title: 'conflict', code: 'conflict' }),
        },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`add-mentor-${ROOM.id}`));
    await fireEvent.press(await screen.findByTestId(`pick-${MEMBER.id}`));
    expect(await screen.findByText(/Only a verified member with the mentor role/)).toBeTruthy();
  });

  it('shows the 501 state in the picker too', async () => {
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        roomsRoute(() => json(200, { items: [ROOM] })),
        { path: '/staff/members', respond: () => json(501, { title: 'Not implemented', code: 'not_implemented' }) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`add-mentor-${ROOM.id}`));
    expect(await screen.findByText(/not available on this server yet/)).toBeTruthy();
  });

  it('removes a mentor after a confirmation', async () => {
    const mentor = ROOM.mentors[0]!;
    mockFetch.mockImplementation(
      router([
        districtsRoute,
        roomsRoute(() => json(200, { items: [ROOM] })),
        { method: 'DELETE', path: mentorPath(mentor.memberId), respond: () => json(204) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`remove-${ROOM.id}-${mentor.memberId}`));
    expect(screen.getByText('Example Saule will no longer be a mentor of Mothers of Demo district A.')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('remove-confirm-confirm'));
    expect(await screen.findByText('Mentor removed.')).toBeTruthy();
    expect(calls().some((r) => r.method === 'DELETE')).toBe(true);
  });
});
