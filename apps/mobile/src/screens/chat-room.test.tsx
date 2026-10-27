import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { ChatRoomScreen } from './chat-room';

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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000003',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const ROOM_ID = '01900000-0000-7000-8000-0000000000r1';
const MSG_ID = '01900000-0000-7000-8000-0000000000g1';
const OTHER_ID = '01900000-0000-7000-8000-0000000000g2';

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderRoom = () =>
  render(
    <Providers>
      <ChatRoomScreen roomId={ROOM_ID} title="Aigul" />
    </Providers>,
  );

beforeEach(async () => {
  mockFetch.mockReset();
  await storage.set('birge.session', 'session-token');
});

describe('ChatRoomScreen', () => {
  it('renders the room title and composer', async () => {
    mockFetch.mockResolvedValue(json(200, []));
    await renderRoom();
    expect(await screen.findByRole('header', { name: 'Aigul' })).toBeTruthy();
    expect(screen.getByLabelText('Message')).toBeTruthy();
  });

  it('shows the empty state when there are no messages', async () => {
    mockFetch.mockResolvedValue(json(200, []));
    await renderRoom();
    expect(await screen.findByText('No messages yet')).toBeTruthy();
  });

  it('shows the error state when history fails to load', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderRoom();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('sends a message with Idempotency-Key and reports another message', async () => {
    const other = {
      id: OTHER_ID,
      roomId: ROOM_ID,
      seq: 1,
      authorId: '01900000-0000-7000-8000-0000000000a1',
      authorName: 'Aigul',
      text: 'Hello there',
      status: 'visible',
      mine: false,
      createdAt: '2026-01-01T00:00:00Z',
    };
    let messages: unknown[] = [other];
    mockFetch.mockImplementation(async (input, init) => {
      const req = input instanceof Request ? input : new Request(String(input), init);
      const url = req.url;
      if (req.method === 'GET' && url.includes(`/chat/rooms/${ROOM_ID}/messages`)) {
        return json(200, messages);
      }
      if (req.method === 'POST' && url.includes(`/chat/rooms/${ROOM_ID}/messages`)) {
        expect(req.headers.get('Idempotency-Key')).toBeTruthy();
        const body = await req.json();
        expect(body.clientId).toBeTruthy();
        expect(body.text).toBe('Hi');
        const sent = {
          id: MSG_ID,
          roomId: ROOM_ID,
          seq: 2,
          authorId: '01900000-0000-7000-8000-0000000000a2',
          authorName: 'Me',
          text: body.text,
          status: 'visible',
          mine: true,
          createdAt: '2026-01-01T00:01:00Z',
        };
        messages = [...messages, sent];
        return json(201, sent);
      }
      if (req.method === 'POST' && url.includes(`/chat/messages/${OTHER_ID}/report`)) {
        const body = await req.json();
        expect(body).toEqual({ reason: 'spam' });
        return json(204);
      }
      return json(500, { title: 'unhandled' });
    });

    await renderRoom();
    expect(await screen.findByText('Hello there')).toBeTruthy();
    await fireEvent.changeText(screen.getByLabelText('Message'), 'Hi');
    await fireEvent.press(screen.getByTestId('chat-send'));
    await waitFor(() =>
      expect(
        mockFetch.mock.calls.some((c) => {
          const req = c[0] instanceof Request ? c[0] : new Request(String(c[0]), c[1]);
          return req.method === 'POST' && req.url.includes(`/chat/rooms/${ROOM_ID}/messages`);
        }),
      ).toBe(true),
    );

    await fireEvent.press(screen.getByTestId(`message-${OTHER_ID}`));
    await fireEvent.press(await screen.findByTestId('report-spam'));
    expect(await screen.findByText('Report sent.')).toBeTruthy();
  });
});
