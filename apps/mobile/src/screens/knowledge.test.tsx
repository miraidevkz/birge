import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { KnowledgeScreen } from './knowledge';

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

const topicId = '01900000-0000-7000-8000-0000000000aa';
const articleId = '01900000-0000-7000-8000-0000000000bb';
const topics = [{ id: topicId, kind: 'rights', code: 'pmpk', title: 'PMPK example' }];
const articles = [{ id: articleId, title: 'How to apply?', status: 'current' }];

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderKnowledge = () =>
  render(
    <Providers>
      <KnowledgeScreen />
    </Providers>,
  );

describe('KnowledgeScreen', () => {
  beforeEach(async () => {
    mockFetch.mockReset();
    await storage.set('birge.session', 'session-token');
  });

  it('renders topics', async () => {
    mockFetch.mockResolvedValue(json(topics));
    await renderKnowledge();
    expect(await screen.findByText('PMPK example')).toBeTruthy();
    expect(screen.getByRole('header', { name: 'Knowledge' })).toBeTruthy();
  });

  it('shows empty when there are no topics', async () => {
    mockFetch.mockResolvedValue(json([]));
    await renderKnowledge();
    expect(await screen.findByText('Topics coming soon')).toBeTruthy();
  });

  it('shows the error state when topics fail to load', async () => {
    mockFetch.mockResolvedValue(json({ code: 'unknown', title: 'error' }, 500));
    await renderKnowledge();
    expect(await screen.findByText('Something went wrong')).toBeTruthy();
  });

  it('opens a topic and navigates to an article', async () => {
    const { router } = require('expo-router');
    mockFetch.mockImplementation(async (input) => {
      const req = input as Request;
      const url = typeof req.url === 'string' ? req.url : String(req.url);
      if (url.includes('/articles')) return json(articles);
      return json(topics);
    });
    await renderKnowledge();
    await fireEvent.press(await screen.findByText('PMPK example'));
    expect(await screen.findByText('How to apply?')).toBeTruthy();
    await fireEvent.press(screen.getByText('How to apply?'));
    await waitFor(() => expect(router.push).toHaveBeenCalledWith(`/knowledge-article/${articleId}`));
  });
});
