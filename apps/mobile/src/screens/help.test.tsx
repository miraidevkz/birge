import { afterAll, beforeAll, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { Linking } from 'react-native';

import { outbox } from '@/lib/help-outbox';
import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { HelpScreen } from './help';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
}));

// The real API client over a fetch the test controls.
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

const renderHelp = () =>
  render(
    <Providers>
      <HelpScreen source="menu" />
    </Providers>,
  );

beforeAll(() => {
  // The outbox schedules retries; keep timers under test control.
  jest.useFakeTimers();
});
afterAll(() => {
  jest.useRealTimers();
});

describe('HelpScreen', () => {
  it('signed out: shows 112 and dials it; crisis text and numbers at once, without any request', async () => {
    const openURL = jest.spyOn(Linking, 'openURL').mockResolvedValue(undefined as never);
    await renderHelp();
    expect(screen.getByRole('header', { name: 'Help now' })).toBeTruthy();
    expect(screen.getByText(/threat to life — call 112/)).toBeTruthy();
    expect(screen.getByText(/You are not alone/)).toBeTruthy();
    expect(screen.getByLabelText('Call Example crisis line, +70000000000')).toBeTruthy();
    expect(await screen.findByText(/Sending a request to a moderator needs sign-in/)).toBeTruthy();
    expect(screen.getByTestId('help-sign-in')).toBeTruthy();
    expect(screen.queryByTestId('help-contact')).toBeNull();
    expect(mockFetch).not.toHaveBeenCalled();
    await fireEvent.press(screen.getByTestId('call-112'));
    expect(openURL).toHaveBeenCalledWith('tel:112');
    openURL.mockRestore();
  });

  it('signed out offline: still shows 112 and dials it without the network', async () => {
    const openURL = jest.spyOn(Linking, 'openURL').mockResolvedValue(undefined as never);
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));
    await renderHelp();
    expect(screen.getByText(/threat to life — call 112/)).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Call 112' }));
    expect(openURL).toHaveBeenCalledWith('tel:112');
    expect(mockFetch).not.toHaveBeenCalled();
    openURL.mockRestore();
  });

  it('offline: the request is stored, the member is told, and the numbers stay', async () => {
    await storage.set('birge.session', 'session-token');
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));
    await renderHelp();
    await fireEvent.press(await screen.findByRole('button', { name: 'Contact a person now' }));

    expect(await screen.findByText(/Your request is saved and will be sent automatically/)).toBeTruthy();
    expect(screen.getByLabelText('Call Example crisis line, +70000000000')).toBeTruthy();
    expect(screen.getByTestId('call-112')).toBeTruthy();
    const pending = await outbox.pending();
    expect(pending).toEqual([expect.objectContaining({ op: 'requestHumanHelp', body: { source: 'menu' } })]);
  });

  it('online: the queued request goes out with its original key and new numbers are shown', async () => {
    const [queued] = await outbox.pending();
    mockFetch.mockReset();
    mockFetch.mockResolvedValue(
      new Response(
        JSON.stringify({
          escalationId: '01900000-0000-7000-8000-0000000000aa',
          crisisPhones: [{ number: '+70000000001', label: 'Duty line' }],
        }),
        { status: 202, headers: { 'Content-Type': 'application/json' } },
      ),
    );
    await renderHelp();
    await fireEvent.press(await screen.findByRole('button', { name: 'Contact a person now' }));

    expect(await screen.findByText(/Request sent/)).toBeTruthy();
    await waitFor(() => expect(screen.getByLabelText('Call Duty line, +70000000001')).toBeTruthy());
    const request = mockFetch.mock.calls[0]?.[0] as Request;
    expect(request.headers.get('Idempotency-Key')).toBe(queued?.key);
    expect(await outbox.pending()).toEqual([]);
  });
});
