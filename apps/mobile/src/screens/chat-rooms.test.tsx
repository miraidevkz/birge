import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { ChatRoomsScreen } from './chat-rooms';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn() },
}));

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  const { getSessionToken } = require('@/lib/session');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'android',
      session: getSessionToken,
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000002',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const ROOM_ID = '01900000-0000-7000-8000-0000000000r1';
const JOIN_ID = '01900000-0000-7000-8000-0000000000r2';

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderRooms = () =>
  render(
    <Providers>
      <ChatRoomsScreen />
    </Providers>,
  );

beforeEach(async () => {
  mockFetch.mockReset();
  await storage.set('birge.session', 'session-token');
});

describe('ChatRoomsScreen', () => {
  it('renders the chats title', async () => {
    mockFetch.mockImplementation(async (input) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.includes('joined=true')) return json(200, []);
      if (url.includes('joined=false')) return json(200, []);
      return json(500, { title: 'unhandled' });
    });
    await renderRooms();
    expect(await screen.findByRole('header', { name: 'Chats' })).toBeTruthy();
  });

  it('tells an unverified member why there are no chats and where to ask', async () => {
    mockFetch.mockImplementation(async (input) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.includes('joined=')) return json(200, []);
      if (url.endsWith('/v1/me'))
        return json(200, {
          id: '01900000-0000-7000-8000-000000000010',
          lang: 'en',
          verification: 'new',
          roles: ['mom'],
          children: [],
        });
      return json(500, { title: 'unhandled' });
    });
    await renderRooms();
    expect(await screen.findByText('Chats open once you are verified')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Go to profile' })).toBeTruthy();
  });

  it('shows the empty state when there are no joined rooms', async () => {
    mockFetch.mockImplementation(async (input) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.includes('joined=true')) return json(200, []);
      if (url.includes('joined=false')) return json(200, []);
      return json(500, { title: 'unhandled' });
    });
    await renderRooms();
    expect(await screen.findByText('No chats yet')).toBeTruthy();
  });

  it('shows the error state when the room list fails', async () => {
    mockFetch.mockImplementation(async (input) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.includes('joined=true')) return json(500, { title: 'server error', code: 'internal' });
      if (url.includes('joined=false')) return json(200, []);
      return json(500, { title: 'unhandled' });
    });
    await renderRooms();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('lists a joined room and joins a discoverable room', async () => {
    let joined = [
      {
        id: ROOM_ID,
        kind: 'direct',
        title: 'Aigul',
        joined: true,
        muted: false,
      },
    ];
    mockFetch.mockImplementation(async (input, init) => {
      const req = input instanceof Request ? input : new Request(String(input), init);
      const url = req.url;
      if (req.method === 'GET' && url.includes('joined=true')) return json(200, joined);
      if (req.method === 'GET' && url.includes('joined=false')) {
        return json(200, [{ id: JOIN_ID, kind: 'district', title: 'District chat', joined: false, muted: false }]);
      }
      if (req.method === 'POST' && url.includes(`/chat/rooms/${JOIN_ID}/join`)) {
        joined = [...joined, { id: JOIN_ID, kind: 'district', title: 'District chat', joined: true, muted: false }];
        return json(200, joined[1]);
      }
      return json(500, { title: 'unhandled' });
    });

    await renderRooms();
    expect(await screen.findByTestId(`room-${ROOM_ID}`)).toBeTruthy();
    expect(screen.getByText('Aigul')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`join-${JOIN_ID}`));
    await waitFor(() =>
      expect(
        mockFetch.mock.calls.some((c) => {
          const input = c[0];
          const url = input instanceof Request ? input.url : String(input);
          return url.includes(`/chat/rooms/${JOIN_ID}/join`);
        }),
      ).toBe(true),
    );
  });
});
