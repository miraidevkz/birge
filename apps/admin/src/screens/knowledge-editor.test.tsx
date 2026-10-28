import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { KnowledgeEditorScreen } from './knowledge-editor';

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
  useLocalSearchParams: jest.fn(() => ({})),
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

const TOPIC = {
  id: '01900000-0000-7000-8000-0000000000t1',
  kind: 'selfhelp',
  code: 'breathing',
  title: 'Breathing',
};

const DRAFT = {
  versionId: '01900000-0000-7000-8000-0000000000v1',
  articleId: '01900000-0000-7000-8000-0000000000a1',
  topicId: TOPIC.id,
  kind: 'selfhelp',
  lang: 'kk',
  status: 'draft',
  content: { title: 'Calm breathing', body: 'Breathe slowly.' },
  authorId: '01900000-0000-7000-8000-0000000000u1',
  updatedAt: '2026-10-28T08:00:00Z',
};

const renderScreen = () =>
  render(
    <Providers>
      <KnowledgeEditorScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'psy-1', roles: ['psychologist'] });
  const { useLocalSearchParams } = require('expo-router') as {
    useLocalSearchParams: jest.Mock;
  };
  useLocalSearchParams.mockReturnValue({});
});

describe('KnowledgeEditorScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, [TOPIC]));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'New draft' })).toBeTruthy();
  });

  it('shows validation when fields are empty', async () => {
    mockFetch.mockResolvedValue(json(200, [TOPIC]));
    await renderScreen();
    await screen.findByTestId('editor-save');
    await fireEvent.press(screen.getByTestId('editor-save'));
    expect(await screen.findByText('Check the fields and try again.')).toBeTruthy();
  });

  it('shows the error state when topics fail to load', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('saves a new draft', async () => {
    mockFetch.mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.includes('/staff/knowledge/topics')) return json(200, [TOPIC]);
      if (url.includes('/staff/knowledge/articles') && !(input instanceof Request && input.method === 'POST')) {
        return json(200, { items: [] });
      }
      return json(201, DRAFT);
    });
    await renderScreen();
    await screen.findByTestId('editor-topic');
    await fireEvent.changeText(screen.getByTestId('editor-title'), 'Calm breathing');
    await fireEvent.changeText(screen.getByTestId('editor-body'), 'Breathe slowly.');
    await fireEvent.press(screen.getByTestId('editor-save'));
    await waitFor(() => {
      const createCall = mockFetch.mock.calls.find((call) => {
        const req = call[0] as Request;
        return req.method === 'POST' && req.url.includes('/staff/knowledge/articles');
      });
      expect(createCall).toBeTruthy();
    });
    expect(await screen.findByText('Draft saved.')).toBeTruthy();
  });

  it('shows a forbidden problem on submit', async () => {
    const { useLocalSearchParams } = require('expo-router') as {
      useLocalSearchParams: jest.Mock;
    };
    useLocalSearchParams.mockReturnValue({ versionId: DRAFT.versionId });
    mockFetch
      .mockResolvedValueOnce(json(200, [TOPIC]))
      .mockResolvedValueOnce(json(200, { items: [DRAFT] }))
      .mockResolvedValueOnce(json(403, { title: 'Not allowed.', code: 'forbidden' }));
    await renderScreen();
    expect(await screen.findByDisplayValue('Calm breathing')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('editor-submit'));
    expect(await screen.findByTestId('editor-problem')).toBeTruthy();
    expect(
      screen.getByText(
        /This action belongs to another role\. Another reviewer may be needed, or you authored this version\./,
      ),
    ).toBeTruthy();
  });
});
