import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { ScreenBackProvider } from '@birge/ui';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { KnowledgeArticleScreen } from './knowledge-article';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000000',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const articleId = '01900000-0000-7000-8000-0000000000bb';
const article = {
  id: articleId,
  topicId: '01900000-0000-7000-8000-0000000000aa',
  kind: 'rights',
  lang: 'en',
  title: 'How to apply?',
  body: 'Submit the form at the local office.',
  reviewedAt: '2026-01-15T00:00:00Z',
  status: 'current',
  sourceUrl: 'https://example.invalid/source',
};

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderArticle = () =>
  render(
    <Providers>
      <KnowledgeArticleScreen articleId={articleId} />
    </Providers>,
  );

describe('KnowledgeArticleScreen', () => {
  beforeEach(async () => {
    mockFetch.mockReset();
    await storage.set('birge.session', 'session-token');
  });

  it('renders the article body', async () => {
    mockFetch.mockResolvedValue(json(article));
    await renderArticle();
    expect(await screen.findByText('Submit the form at the local office.')).toBeTruthy();
    expect(screen.getByRole('header', { name: 'How to apply?' })).toBeTruthy();
  });

  it('shows not found on error', async () => {
    mockFetch.mockResolvedValue(json({ code: 'not_found', title: 'missing' }, 404));
    await renderArticle();
    expect(await screen.findByText('Article not found.')).toBeTruthy();
  });

  it('goes back through the screen header when the navigator provides Back', async () => {
    const onBack = jest.fn();
    mockFetch.mockResolvedValue(json(article));
    await render(
      <Providers>
        <ScreenBackProvider onBack={onBack}>
          <KnowledgeArticleScreen articleId={articleId} />
        </ScreenBackProvider>
      </Providers>,
    );
    await screen.findByText('Submit the form at the local office.');
    await fireEvent.press(screen.getByTestId('screen-back'));
    await waitFor(() => expect(onBack).toHaveBeenCalled());
  });
});
