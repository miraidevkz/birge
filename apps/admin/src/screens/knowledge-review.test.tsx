import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { KnowledgeReviewScreen } from './knowledge-review';

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

function json(status: number, body?: unknown): Response {
  if (body === undefined) return new Response(null, { status });
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': status >= 400 ? 'application/problem+json' : 'application/json' },
  });
}

const ITEM = {
  versionId: '01900000-0000-7000-8000-0000000000v2',
  articleId: '01900000-0000-7000-8000-0000000000a2',
  topicId: '01900000-0000-7000-8000-0000000000t2',
  kind: 'rights',
  lang: 'ru',
  status: 'in_review',
  content: { title: 'Benefit claim steps', body: 'Step one.' },
  authorId: '01900000-0000-7000-8000-0000000000u2',
  updatedAt: '2026-10-28T09:00:00Z',
};

const renderScreen = () =>
  render(
    <Providers>
      <KnowledgeReviewScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'lawyer-1', roles: ['lawyer'] });
});

describe('KnowledgeReviewScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Review queue' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('Nothing to review')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('publishes a version', async () => {
    mockFetch
      .mockResolvedValueOnce(json(200, { items: [ITEM] }))
      .mockResolvedValueOnce(json(200, { ...ITEM, status: 'published' }))
      .mockResolvedValueOnce(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('Benefit claim steps')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`publish-${ITEM.versionId}`));
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(3));
    const publishCall = mockFetch.mock.calls[1]![0] as Request;
    expect(publishCall.method).toBe('POST');
    expect(publishCall.url).toContain(`/staff/knowledge/versions/${ITEM.versionId}/publish`);
  });

  it('shows a forbidden problem when publish is denied', async () => {
    mockFetch
      .mockResolvedValueOnce(json(200, { items: [ITEM] }))
      .mockResolvedValueOnce(json(403, { title: 'Not allowed.', code: 'forbidden' }));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId(`publish-${ITEM.versionId}`));
    expect(await screen.findByTestId('review-problem')).toBeTruthy();
    expect(
      screen.getByText(
        /You are not allowed to do this\. Another reviewer may be required, or you authored this version\./,
      ),
    ).toBeTruthy();
  });
});
