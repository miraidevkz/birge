import { afterEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { Providers } from '@/test-utils';

import { InboxScreen } from './inbox';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
  useFocusEffect: jest.fn(),
}));

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'android',
      session: async () => 'session-token',
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000003',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const ROOM = '01900000-0000-7000-8000-000000000077';

function item(n: number, template: string, extra: Record<string, unknown> = {}) {
  return {
    id: `01900000-0000-7000-8000-0000000001${String(n).padStart(2, '0')}`,
    template,
    params: {},
    class: 'normal',
    createdAt: '2026-10-20T10:00:00Z',
    ...extra,
  };
}

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

/** serve answers GET /me/inbox by cursor and 204 to every write. */
function serve(pages: Record<string, unknown>) {
  mockFetch.mockImplementation(async (input) => {
    const req = input as Request;
    if (req.method !== 'GET') return new Response(null, { status: 204 });
    const cursor = new URL(req.url).searchParams.get('cursor') ?? '';
    return cursor in pages ? json(pages[cursor]) : json({}, 404);
  });
}

const writes = () => mockFetch.mock.calls.map(([i]) => i as Request).filter((r) => r.method !== 'GET');

const renderInbox = () =>
  render(
    <Providers>
      <InboxScreen />
    </Providers>,
  );

afterEach(() => {
  mockFetch.mockReset();
  jest.clearAllMocks();
});

describe('InboxScreen', () => {
  it('renders items by template with an unread style and the time', async () => {
    serve({
      '': {
        items: [
          item(1, 'chat.new_message', { params: { room_id: ROOM } }),
          item(2, 'wellbeing.checkin_reminder', { readAt: '2026-10-21T10:00:00Z' }),
        ],
      },
    });
    await renderInbox();
    expect(await screen.findByText('New message')).toBeTruthy();
    expect(screen.getByText('New message in your chat room.')).toBeTruthy();
    expect(screen.getByText('Weekly check-in')).toBeTruthy();
    const unread = screen.getByTestId(`inbox-item-${item(1, '').id}`);
    expect(unread.props.accessibilityLabel).toMatch(/^New message, New message in your chat room\., .+, Unread$/);
    const read = screen.getByTestId(`inbox-item-${item(2, '').id}`);
    expect(read.props.accessibilityLabel).not.toMatch(/Unread/);
    expect(screen.getAllByText(/Oct/).length).toBe(2);
  });

  it('shows a generic title for an unknown template, never the raw code', async () => {
    serve({ '': { items: [item(1, 'future.something_new')] } });
    await renderInbox();
    expect(await screen.findByText('New notification')).toBeTruthy();
    expect(screen.getByText('There is something new for you.')).toBeTruthy();
    expect(screen.queryByText(/future\.something_new/)).toBeNull();
  });

  it('shows the warm empty state with a next step', async () => {
    const { router } = require('expo-router');
    serve({ '': { items: [] } });
    await renderInbox();
    expect(await screen.findByText('All quiet for now')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Open the Circle' }));
    expect(router.push).toHaveBeenCalledWith('/circle');
  });

  it('shows the error state and retries', async () => {
    mockFetch.mockImplementation(async () => json({ code: 'unavailable' }, 503));
    await renderInbox();
    expect(await screen.findByText('Something went wrong')).toBeTruthy();
    serve({ '': { items: [item(1, 'circle.ask_met')] } });
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByText('Did you meet?')).toBeTruthy();
  });

  it('marks an item read and opens its place in the app', async () => {
    const { router } = require('expo-router');
    const first = item(1, 'chat.new_message', { params: { room_id: ROOM } });
    serve({ '': { items: [first] } });
    await renderInbox();
    await fireEvent.press(await screen.findByTestId(`inbox-item-${first.id}`));
    expect(router.push).toHaveBeenCalledWith(`/chat/${ROOM}`);
    await waitFor(() => expect(writes()).toHaveLength(1));
    expect(writes()[0]!.method).toBe('POST');
    expect(new URL(writes()[0]!.url).pathname).toBe(`/v1/me/inbox/${first.id}/read`);
  });

  it('marks an item without a place read and stays; a read item sends nothing', async () => {
    const { router } = require('expo-router');
    const invite = item(1, 'emergency.trusted_invitation');
    const old = item(2, 'circle.ask_met', { readAt: '2026-10-21T10:00:00Z' });
    serve({ '': { items: [invite, old] } });
    await renderInbox();
    await fireEvent.press(await screen.findByTestId(`inbox-item-${invite.id}`));
    await waitFor(() => expect(writes()).toHaveLength(1));
    expect(router.push).not.toHaveBeenCalled();
    await fireEvent.press(screen.getByTestId(`inbox-item-${old.id}`));
    expect(router.push).toHaveBeenCalledWith('/circle');
    expect(writes()).toHaveLength(1);
  });

  it('pages with the cursor', async () => {
    serve({
      '': { items: [item(1, 'circle.ask_met')], nextCursor: 'c2' },
      c2: { items: [item(2, 'sharing.listing_decided')] },
    });
    await renderInbox();
    await fireEvent.press(await screen.findByTestId('inbox-list-more'));
    expect(await screen.findByText('Listing reviewed')).toBeTruthy();
    expect(screen.queryByTestId('inbox-list-more')).toBeNull();
  });
});
