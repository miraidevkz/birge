import { afterAll, afterEach, beforeAll, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { Linking } from 'react-native';

import { outbox } from '@/lib/help-outbox';
import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { SosScreen } from './sos';

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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000004',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const renderSos = () =>
  render(
    <Providers>
      <SosScreen />
    </Providers>,
  );

beforeAll(() => {
  jest.useFakeTimers();
});
afterAll(() => {
  jest.useRealTimers();
});
afterEach(() => {
  mockFetch.mockReset();
});

describe('SosScreen', () => {
  it('render: shows 112 and crisis phones at once without waiting for the network', async () => {
    const openURL = jest.spyOn(Linking, 'openURL').mockResolvedValue(undefined as never);
    mockFetch.mockImplementation(() => new Promise(() => undefined));
    await renderSos();
    expect(screen.getByRole('header', { name: 'SOS' })).toBeTruthy();
    expect(screen.getByText(/threat to life — call 112/)).toBeTruthy();
    expect(screen.getByLabelText('Call Example crisis line, +70000000000')).toBeTruthy();
    expect(mockFetch).not.toHaveBeenCalled();
    await fireEvent.press(screen.getByTestId('call-112'));
    expect(openURL).toHaveBeenCalledWith('tel:112');
    openURL.mockRestore();
  });

  it('signed out: explains sign-in is needed for requests; 112 and phones stay', async () => {
    const openURL = jest.spyOn(Linking, 'openURL').mockResolvedValue(undefined as never);
    await renderSos();
    expect(await screen.findByText(/Sending an SOS or contacting a moderator needs sign-in/)).toBeTruthy();
    expect(screen.getByTestId('sos-sign-in')).toBeTruthy();
    expect(screen.queryByTestId('sos-type-other')).toBeNull();
    expect(screen.getByLabelText('Call Example crisis line, +70000000000')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Call 112' }));
    expect(openURL).toHaveBeenCalledWith('tel:112');
    openURL.mockRestore();
  });

  it('signed out offline: still shows 112 and dials it without the network', async () => {
    const openURL = jest.spyOn(Linking, 'openURL').mockResolvedValue(undefined as never);
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));
    await renderSos();
    expect(screen.getByText(/threat to life — call 112/)).toBeTruthy();
    await fireEvent.press(screen.getByTestId('call-112'));
    expect(openURL).toHaveBeenCalledWith('tel:112');
    expect(mockFetch).not.toHaveBeenCalled();
    openURL.mockRestore();
  });

  it('error: raise failure shows a danger banner; phones stay', async () => {
    mockFetch.mockImplementation(async (input) => {
      const req = input instanceof Request ? input : null;
      const url = String(req?.url ?? input);
      if (url.match(/\/me\/sos$/) && req?.method === 'GET') return json([]);
      if (url.match(/\/me\/sos$/) && req?.method === 'POST') {
        return json({ title: 'down' }, 500);
      }
      return json([]);
    });
    await storage.set('birge.session', 'session-token');
    await renderSos();
    await fireEvent.press(await screen.findByRole('button', { name: 'Other' }));
    await fireEvent.changeText(await screen.findByLabelText('Short message'), 'Need help with transport.');
    await fireEvent.press(screen.getByRole('button', { name: 'Send SOS' }));
    expect(await screen.findByText(/Could not send the SOS/)).toBeTruthy();
    expect(screen.getByLabelText('Call Example crisis line, +70000000000')).toBeTruthy();
    expect(screen.getByTestId('call-112')).toBeTruthy();
  });

  it('main action: raises an SOS when signed in', async () => {
    mockFetch.mockImplementation(async (input) => {
      const req = input instanceof Request ? input : null;
      const url = String(req?.url ?? input);
      if (url.match(/\/me\/sos$/) && (!req || req.method === 'GET')) return json([]);
      if (url.match(/\/me\/sos$/) && req?.method === 'POST') {
        return json(
          {
            id: '01900000-0000-7000-8000-0000000000bb',
            type: 'other',
            text: 'Need help with transport.',
            status: 'open',
            sharedToDistrict: false,
            createdAt: '2026-10-28T00:00:00Z',
            responseCount: 0,
          },
          201,
        );
      }
      return json([]);
    });
    await storage.set('birge.session', 'session-token');
    await renderSos();
    await fireEvent.press(await screen.findByRole('button', { name: 'Other' }));
    await fireEvent.changeText(await screen.findByLabelText('Short message'), 'Need help with transport.');
    await fireEvent.press(screen.getByRole('button', { name: 'Send SOS' }));
    expect(await screen.findByText(/SOS sent/)).toBeTruthy();
    await waitFor(() => {
      const post = mockFetch.mock.calls.find((c) => c[0] instanceof Request && (c[0] as Request).method === 'POST');
      expect(post).toBeTruthy();
    });
  });

  it('feel bad: queues requestHumanHelp with source sos', async () => {
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));
    await storage.set('birge.session', 'session-token');
    await renderSos();
    await fireEvent.press(await screen.findByRole('button', { name: 'I feel very bad' }));
    expect(await screen.findByText(/Your request to a person on duty is saved/)).toBeTruthy();
    const pending = await outbox.pending();
    expect(pending).toEqual([expect.objectContaining({ op: 'requestHumanHelp', body: { source: 'sos' } })]);
  });
});
