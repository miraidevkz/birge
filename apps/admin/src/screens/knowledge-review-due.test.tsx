import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { KnowledgeReviewDueScreen } from './knowledge-review-due';

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
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
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
  versionId: '01900000-0000-7000-8000-0000000000v3',
  articleId: '01900000-0000-7000-8000-0000000000a3',
  topicId: '01900000-0000-7000-8000-0000000000t3',
  kind: 'rights',
  lang: 'en',
  status: 'published',
  content: { title: 'School inclusion FAQ', body: 'Answer text.' },
  authorId: '01900000-0000-7000-8000-0000000000u3',
  expiresAt: '2026-11-01T00:00:00Z',
  updatedAt: '2026-04-01T00:00:00Z',
};

const renderScreen = () =>
  render(
    <Providers>
      <KnowledgeReviewDueScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'lawyer-1', roles: ['lawyer'] });
});

describe('KnowledgeReviewDueScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Review due' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('Nothing due')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('opens the editor for a new version', async () => {
    const { router } = require('expo-router') as { router: { push: jest.Mock } };
    mockFetch.mockResolvedValue(json(200, { items: [ITEM] }));
    await renderScreen();
    expect(await screen.findByText('School inclusion FAQ')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`new-version-${ITEM.versionId}`));
    expect(router.push).toHaveBeenCalledWith({
      pathname: '/knowledge/editor',
      params: { articleId: ITEM.articleId },
    });
  });

  it('shows a forbidden problem when the list is denied', async () => {
    mockFetch.mockResolvedValue(json(403, { title: 'Not allowed.', code: 'forbidden' }));
    await renderScreen();
    expect(await screen.findByTestId('review-due-problem')).toBeTruthy();
    expect(
      screen.getByText(
        /This action belongs to another role\. Another reviewer may be needed, or you authored this version\./,
      ),
    ).toBeTruthy();
  });
});
