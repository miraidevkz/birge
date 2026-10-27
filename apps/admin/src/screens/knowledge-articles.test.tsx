import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { KnowledgeArticlesScreen } from './knowledge-articles';

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

const ARTICLE = {
  versionId: '01900000-0000-7000-8000-0000000000v1',
  articleId: '01900000-0000-7000-8000-0000000000a1',
  topicId: '01900000-0000-7000-8000-0000000000t1',
  kind: 'selfhelp',
  lang: 'kk',
  status: 'draft',
  content: { title: 'Example self-help draft', body: 'Body text' },
  authorId: '01900000-0000-7000-8000-0000000000u1',
  updatedAt: '2026-10-28T08:00:00Z',
};

const renderScreen = () =>
  render(
    <Providers>
      <KnowledgeArticlesScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'psy-1', roles: ['psychologist'] });
});

describe('KnowledgeArticlesScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Knowledge' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderScreen();
    expect(await screen.findByText('No articles')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('opens the new draft editor', async () => {
    const { router } = require('expo-router') as { router: { push: jest.Mock } };
    mockFetch.mockResolvedValue(json(200, { items: [ARTICLE] }));
    await renderScreen();
    expect(await screen.findByText('Example self-help draft')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('knowledge-new-draft'));
    expect(router.push).toHaveBeenCalledWith('/knowledge/editor');
  });

  it('shows a forbidden problem when topic create is denied', async () => {
    mockFetch
      .mockResolvedValueOnce(json(200, { items: [] }))
      .mockResolvedValueOnce(json(403, { title: 'Not allowed.', code: 'forbidden' }));
    await renderScreen();
    await fireEvent.press(await screen.findByTestId('knowledge-create-topic'));
    await fireEvent.changeText(screen.getByTestId('topic-code'), 'demo_topic');
    await fireEvent.changeText(screen.getByTestId('topic-title-en'), 'Demo topic');
    await fireEvent.press(screen.getByTestId('topic-save'));
    expect(
      await screen.findByText(
        /You are not allowed to do this\. Another reviewer may be required, or you authored this version\./,
      ),
    ).toBeTruthy();
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(2));
  });
});
