import { afterEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { CheckinScreen } from './checkin';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), back: jest.fn(), replace: jest.fn() },
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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000001',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const questionnaire = {
  version: 'example-v1',
  questions: [
    {
      id: 'phq_1',
      instrument: 'phq4',
      textKey: 'wellbeing.phq4.q1',
      options: [
        { value: 0, textKey: 'wellbeing.phq4.opt.0' },
        { value: 1, textKey: 'wellbeing.phq4.opt.1' },
      ],
    },
  ],
};

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const renderCheckin = () =>
  render(
    <Providers>
      <CheckinScreen />
    </Providers>,
  );

afterEach(() => {
  mockFetch.mockReset();
});

describe('CheckinScreen', () => {
  it('render: shows start and loads the questionnaire', async () => {
    mockFetch.mockResolvedValue(json(questionnaire));
    await storage.set('birge.session', 'session-token');
    await renderCheckin();
    expect(screen.getByRole('header', { name: 'How am I' })).toBeTruthy();
    expect(await screen.findByRole('button', { name: 'Start check-in' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Past check-ins' })).toBeTruthy();
  });

  it('empty questionnaire: start stays disabled while questions are missing', async () => {
    mockFetch.mockResolvedValue(json({ version: 'empty', questions: [] }));
    await storage.set('birge.session', 'session-token');
    await renderCheckin();
    const start = await screen.findByRole('button', { name: 'Start check-in' });
    expect(start).toBeDisabled();
  });

  it('error: shows retry when the questionnaire fails to load', async () => {
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));
    await storage.set('birge.session', 'session-token');
    await renderCheckin();
    expect(await screen.findByText('Something went wrong')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
    expect(startDisabled()).toBe(true);
  });

  it('main action: answers a question and shows the result', async () => {
    mockFetch.mockImplementation(async (input) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.includes('/me/checkins/questionnaire')) return json(questionnaire);
      if (url.endsWith('/me/checkins') && !(input instanceof Request && input.method === 'GET')) {
        return json(
          {
            id: '01900000-0000-7000-8000-0000000000aa',
            flag: 'none',
            feedbackKey: 'wellbeing.feedback.none',
            suggestion: 'practice',
          },
          201,
        );
      }
      return json({});
    });
    await storage.set('birge.session', 'session-token');
    await renderCheckin();
    await screen.findByRole('button', { name: 'Start check-in' });
    // Start stays disabled until the questionnaire has loaded; a press before
    // that is ignored, which made this test fail about one run in three.
    await waitFor(() => expect(startDisabled()).toBe(false));
    await fireEvent.press(screen.getByRole('button', { name: 'Start check-in' }));
    expect(await screen.findByText(/Little interest or pleasure/)).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Not at all' }));
    expect(await screen.findByTestId('checkin-result')).toBeTruthy();
    expect(screen.getByText(/Thank you for checking in/)).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Done' })).toBeTruthy());
  });
});

function startDisabled(): boolean {
  const start = screen.getByRole('button', { name: 'Start check-in' });
  return !!start.props.accessibilityState?.disabled;
}
