import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { PrivacyScreen } from './privacy';

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
    kratos: { signOut: jest.fn(async () => undefined) },
  };
});

const consents = [
  { type: 'data_processing', granted: true, currentVersion: 'v1' },
  { type: 'ai_crossborder', granted: false, currentVersion: 'v1' },
  { type: 'research', granted: false, currentVersion: 'v1' },
];

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderPrivacy = () =>
  render(
    <Providers>
      <PrivacyScreen />
    </Providers>,
  );

describe('PrivacyScreen', () => {
  beforeEach(async () => {
    mockFetch.mockReset();
    jest.clearAllMocks();
    await storage.set('birge.session', 'session-token');
  });

  it('renders consents', async () => {
    mockFetch.mockResolvedValue(json(consents));
    await renderPrivacy();
    expect(await screen.findByText('Processing of personal data')).toBeTruthy();
    expect(screen.getByText('Sending anonymized text to the AI abroad')).toBeTruthy();
  });

  it('shows empty when there are no consents', async () => {
    mockFetch.mockResolvedValue(json([]));
    await renderPrivacy();
    expect(await screen.findByText('No consents yet')).toBeTruthy();
  });

  it('shows the error state when consents fail to load', async () => {
    mockFetch.mockResolvedValue(json({ code: 'unknown', title: 'error' }, 500));
    await renderPrivacy();
    expect(await screen.findByText('Something went wrong')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Try again' })).toBeTruthy();
  });

  it('opens the deletion sheet and confirms deletion', async () => {
    const { router } = require('expo-router');
    mockFetch.mockImplementation(async (input) => {
      const req = input as Request;
      const url = typeof req.url === 'string' ? req.url : String(req.url);
      if (url.includes('/me/deletion-request')) {
        return json({ requestedAt: '2026-10-28T00:00:00Z', dueAt: '2026-10-29T00:00:00Z' }, 202);
      }
      return json(consents);
    });
    await renderPrivacy();
    await screen.findByText('Processing of personal data');
    await fireEvent.press(screen.getByTestId('privacy-delete-open'));
    expect(await screen.findByText(/Once deleted, your data cannot be restored/)).toBeTruthy();
    await fireEvent.press(screen.getByTestId('privacy-delete-confirm'));
    // Success: signed out first, then the calm final screen, never the failure banner.
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/account-deleted'));
    expect(await storage.get('birge.session')).toBeNull();
    expect(screen.queryByText('Could not schedule deletion. Try again.')).toBeNull();
  });

  it('keeps her on the sheet with a retry when the deletion request fails', async () => {
    const { router } = require('expo-router');
    mockFetch.mockImplementation(async (input) => {
      const req = input as Request;
      if (String(req.url).includes('/me/deletion-request')) return json({ code: 'unknown', title: 'error' }, 500);
      return json(consents);
    });
    await renderPrivacy();
    await screen.findByText('Processing of personal data');
    await fireEvent.press(screen.getByTestId('privacy-delete-open'));
    await fireEvent.press(screen.getByTestId('privacy-delete-confirm'));
    expect(await screen.findByText('Could not schedule deletion. Try again.')).toBeTruthy();
    expect(router.replace).not.toHaveBeenCalled();
    expect(await storage.get('birge.session')).toBe('session-token');
  });
});
