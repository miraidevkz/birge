import { afterEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { EmergencyPlanScreen } from './emergency-plan';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());

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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000003',
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

const emptyPlan = { childCard: '', contacts: [] };

const renderPlan = () =>
  render(
    <Providers>
      <EmergencyPlanScreen />
    </Providers>,
  );

afterEach(() => {
  mockFetch.mockReset();
});

describe('EmergencyPlanScreen', () => {
  it('render: shows the plan form after load', async () => {
    mockFetch.mockResolvedValue(json(emptyPlan));
    await storage.set('birge.session', 'session-token');
    await renderPlan();
    expect(screen.getByRole('header', { name: 'Emergency plan' })).toBeTruthy();
    expect(await screen.findByLabelText(/Needs, medicines/)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Save child card' })).toBeTruthy();
  });

  it('empty: shows empty contacts state', async () => {
    mockFetch.mockResolvedValue(json(emptyPlan));
    await storage.set('birge.session', 'session-token');
    await renderPlan();
    expect(await screen.findByText('No trusted contacts yet')).toBeTruthy();
  });

  it('error: shows retry when the plan fails to load', async () => {
    mockFetch.mockRejectedValue(new TypeError('Network request failed'));
    await storage.set('birge.session', 'session-token');
    await renderPlan();
    expect(await screen.findByText('Could not load the plan.')).toBeTruthy();
    mockFetch.mockResolvedValue(json(emptyPlan));
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.getByLabelText(/Needs, medicines/)).toBeTruthy());
  });

  it('main action: saves the child card', async () => {
    mockFetch.mockImplementation(async (input) => {
      const req = input instanceof Request ? input : null;
      const url = String(req?.url ?? input);
      if (url.includes('/me/emergency-plan/child-card')) {
        return new Response(null, { status: 204 });
      }
      if (url.includes('/me/emergency-plan')) return json(emptyPlan);
      return json({});
    });
    await storage.set('birge.session', 'session-token');
    await renderPlan();
    await fireEvent.changeText(await screen.findByLabelText(/Needs, medicines/), 'Needs quiet space.');
    await fireEvent.press(screen.getByRole('button', { name: 'Save child card' }));
    await waitFor(() => expect(screen.getByText('Child card saved.')).toBeTruthy());
    const put = mockFetch.mock.calls.find((c) => {
      const req = c[0];
      return req instanceof Request && req.method === 'PUT';
    });
    expect(put).toBeTruthy();
  });
});
