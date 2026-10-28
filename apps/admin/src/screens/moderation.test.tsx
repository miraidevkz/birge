import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { json, Providers, router } from '@/test-utils';

import { ModerationScreen } from './moderation';

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

const ITEM = {
  id: '01900000-0000-7000-8000-0000000000m1',
  kind: 'listing',
  objectId: '01900000-0000-7000-8000-0000000000o1',
  createdAt: '2026-10-28T08:00:00Z',
  summary: 'Example listing summary',
  photoUrl: 'https://media.example.invalid/listing.jpg',
};

const REPORT = {
  id: '01900000-0000-7000-8000-0000000000m2',
  kind: 'report',
  objectId: '01900000-0000-7000-8000-0000000000o2',
  createdAt: '2026-10-28T09:00:00Z',
  summary: 'Example reported message',
  reportReason: 'harassment',
  reportCount: 3,
};

const HELD = {
  id: '01900000-0000-7000-8000-0000000000m3',
  kind: 'chat_message',
  objectId: '01900000-0000-7000-8000-0000000000o3',
  createdAt: '2026-10-28T10:00:00Z',
  summary: 'Example held message',
  holdReason: 'future_code',
};

const VERIFICATION = {
  id: '01900000-0000-7000-8000-0000000000m4',
  kind: 'verification',
  objectId: '01900000-0000-7000-8000-0000000000o4',
  createdAt: '2026-10-28T11:00:00Z',
  summary: 'Example verification request',
};

const renderScreen = () =>
  render(
    <Providers>
      <ModerationScreen />
    </Providers>,
  );

const calls = () => mockFetch.mock.calls.map((c) => c[0] as Request);

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'mod-1', roles: ['moderator'] });
});

describe('ModerationScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Moderation' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('Queue is empty')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('approves a queue item', async () => {
    mockFetch
      .mockResolvedValueOnce(json(200, { items: [ITEM] }))
      .mockResolvedValueOnce(json(204))
      .mockResolvedValueOnce(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('Example listing summary')).toBeTruthy();
    expect(screen.getByLabelText('Photo under review')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`approve-${ITEM.id}`));
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(3));
    const decideCall = calls()[1]!;
    expect(decideCall.method).toBe('POST');
    expect(decideCall.url).toContain(`/staff/moderation/items/${ITEM.id}/decision`);
  });

  it('filters by kind through the kind parameter', async () => {
    mockFetch.mockImplementation(async () => json(200, { items: [] }));
    await renderScreen();
    await screen.findByText('Queue is empty');
    expect(calls()[0]!.url).not.toContain('kind=');
    await fireEvent.press(screen.getByTestId('moderation-kind-report'));
    await waitFor(() => expect(calls().some((r) => r.url.includes('kind=report'))).toBe(true));
  });

  it('loads the next page with the cursor', async () => {
    mockFetch.mockImplementation(
      router([
        {
          path: '/staff/moderation/queue',
          respond: (req) =>
            req.url.includes('cursor=page2')
              ? json(200, { items: [REPORT] })
              : json(200, { items: [ITEM], nextCursor: 'page2' }),
        },
      ]),
    );
    await renderScreen();
    expect(await screen.findByText('Example listing summary')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Show more' }));
    expect(await screen.findByText('Example reported message')).toBeTruthy();
    expect(screen.getByText('Example listing summary')).toBeTruthy();
  });

  it('shows reason codes as labels and never the raw code', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [REPORT, HELD] }));
    await renderScreen();
    expect(await screen.findByText('Reported: Harassment')).toBeTruthy();
    expect(screen.getByText('Open reports: 3')).toBeTruthy();
    expect(screen.getByText('Held: Other reason')).toBeTruthy();
    expect(screen.queryByText(/future_code/)).toBeNull();
  });

  it('rejects a report with blockAuthor and legalHold after a confirmation', async () => {
    const bodies: string[] = [];
    mockFetch.mockImplementation(
      router([
        { path: '/staff/moderation/queue', respond: () => json(200, { items: [REPORT] }) },
        {
          method: 'POST',
          path: `/staff/moderation/items/${REPORT.id}/decision`,
          respond: async (req) => {
            bodies.push(await req.text());
            return json(204);
          },
        },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`reject-${REPORT.id}`));
    await fireEvent.press(screen.getByTestId('reject-reason-unsafe'));
    await fireEvent.press(screen.getByTestId('reject-block-author'));
    await fireEvent.press(screen.getByTestId('reject-legal-hold'));
    await fireEvent.press(screen.getByTestId('confirm-reject'));
    // Suspending her needs one more confirmation; nothing is sent yet.
    expect(await screen.findByText('Reject and suspend the author?')).toBeTruthy();
    expect(bodies).toHaveLength(0);
    await fireEvent.press(screen.getByTestId('block-confirm-confirm'));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(JSON.parse(bodies[0]!)).toEqual({ approve: false, reason: 'unsafe', blockAuthor: true, legalHold: true });
  });

  it('offers no block or legal-hold switches for a listing', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [ITEM] }));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`reject-${ITEM.id}`));
    expect(screen.getByTestId('reject-reason-spam')).toBeTruthy();
    expect(screen.queryByTestId('reject-block-author')).toBeNull();
    expect(screen.queryByTestId('reject-legal-hold')).toBeNull();
    expect(screen.queryByTestId(`hold-on-${ITEM.id}`)).toBeNull();
  });

  it('limits verification to its own approve text and reasons', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [VERIFICATION] }));
    await renderScreen();
    expect(await screen.findByRole('button', { name: 'Approve and verify' })).toBeTruthy();
    expect(screen.getByText('Approving makes her a verified member.')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`reject-${VERIFICATION.id}`));
    expect(screen.getByTestId('reject-reason-inaccurate')).toBeTruthy();
    expect(screen.getByTestId('reject-reason-duplicate')).toBeTruthy();
    expect(screen.getByTestId('reject-reason-other')).toBeTruthy();
    expect(screen.queryByTestId('reject-reason-spam')).toBeNull();
  });

  it('puts the message of a held item on legal hold after a confirmation', async () => {
    mockFetch.mockImplementation(
      router([
        { path: '/staff/moderation/queue', respond: () => json(200, { items: [HELD] }) },
        { method: 'PUT', path: `/staff/chat/messages/${HELD.objectId}/legal-hold`, respond: () => json(204) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`hold-on-${HELD.id}`));
    expect(screen.getByText('Put this message on legal hold?')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('hold-confirm-confirm'));
    expect(await screen.findByText('The message is on legal hold.')).toBeTruthy();
    const put = calls().find((r) => r.method === 'PUT');
    expect(put?.url).toContain(`/staff/chat/messages/${HELD.objectId}/legal-hold`);
  });

  it('releases a legal hold after a confirmation', async () => {
    mockFetch.mockImplementation(
      router([
        { path: '/staff/moderation/queue', respond: () => json(200, { items: [REPORT] }) },
        { method: 'DELETE', path: `/staff/chat/messages/${REPORT.objectId}/legal-hold`, respond: () => json(204) },
      ]),
    );
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`hold-off-${REPORT.id}`));
    await fireEvent.press(screen.getByTestId('hold-confirm-confirm'));
    expect(await screen.findByText('The legal hold is released.')).toBeTruthy();
  });
});
