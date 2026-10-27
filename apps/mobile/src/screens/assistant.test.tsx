import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { AssistantScreen } from './assistant';

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

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderAssistant = () =>
  render(
    <Providers>
      <AssistantScreen />
    </Providers>,
  );

describe('AssistantScreen', () => {
  beforeEach(async () => {
    mockFetch.mockReset();
    await storage.set('birge.session', 'session-token');
  });

  it('renders the empty chat with the disclaimer', async () => {
    await renderAssistant();
    expect(screen.getByRole('header', { name: 'Assistant' })).toBeTruthy();
    expect(screen.getByText(/I am a bot and an AI assistant/)).toBeTruthy();
    expect(screen.getByText('Ask a question')).toBeTruthy();
  });

  it('sends a message and shows the reply', async () => {
    mockFetch.mockResolvedValue(
      json({
        messageId: '01900000-0000-7000-8000-0000000000cc',
        text: 'Here is a grounded answer.',
        crisis: false,
        degraded: false,
        sources: [],
        suggest: 'none',
        remainingToday: 9,
      }),
    );
    await renderAssistant();
    await fireEvent.changeText(screen.getByTestId('assistant-input'), 'How do I apply?');
    await fireEvent.press(screen.getByTestId('assistant-send'));
    expect(await screen.findByText('Here is a grounded answer.')).toBeTruthy();
    expect(screen.getByText('9 messages left today')).toBeTruthy();
  });

  it('shows an error when send fails', async () => {
    mockFetch.mockResolvedValue(json({ code: 'unknown', title: 'error' }, 500));
    await renderAssistant();
    await fireEvent.changeText(screen.getByTestId('assistant-input'), 'Hello');
    await fireEvent.press(screen.getByTestId('assistant-send'));
    expect(await screen.findByText('Could not send. Try again.')).toBeTruthy();
    // A failed send still offers Help: the message might have been a crisis.
    expect(screen.getByTestId('assistant-error-help')).toBeTruthy();
  });

  it('on crisis shows the fixed help text and a Help link', async () => {
    const { router } = require('expo-router');
    mockFetch.mockResolvedValue(
      json({
        messageId: '01900000-0000-7000-8000-0000000000dd',
        crisis: true,
        degraded: false,
        crisisPhones: [{ number: '+70000000002', label: 'Duty' }],
        sources: [],
        suggest: 'none',
      }),
    );
    await renderAssistant();
    await fireEvent.changeText(screen.getByTestId('assistant-input'), 'I cannot go on');
    await fireEvent.press(screen.getByTestId('assistant-send'));
    // The fixed help text is shown once, with 112 and the reply's crisis phones right there.
    expect(await screen.findAllByText(/You are not alone/)).toHaveLength(1);
    expect(screen.getByTestId('call-112')).toBeTruthy();
    expect(await screen.findByText('+70000000002')).toBeTruthy();
    expect(screen.getByTestId('assistant-open-help')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('assistant-open-help'));
    await waitFor(() => expect(router.push).toHaveBeenCalledWith('/sos'));
  });
});
