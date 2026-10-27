import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { ListingsScreen } from './listings';

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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000004',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderListings = () =>
  render(
    <Providers>
      <ListingsScreen />
    </Providers>,
  );

beforeEach(async () => {
  mockFetch.mockReset();
  await storage.set('birge.session', 'session-token');
});

describe('ListingsScreen', () => {
  it('renders the share title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderListings();
    expect(await screen.findByRole('header', { name: 'Share' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderListings();
    expect(await screen.findByText('No listings yet')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderListings();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('lists a listing and retries after an error', async () => {
    mockFetch.mockResolvedValueOnce(json(500, { title: 'server error', code: 'internal' })).mockResolvedValueOnce(
      json(200, {
        items: [
          {
            id: '01900000-0000-7000-8000-0000000000l1',
            type: 'give',
            category: 'clothes',
            title: 'Winter jacket',
            description: 'Size 98',
            districtId: '01900000-0000-7000-8000-0000000000d1',
            status: 'active',
            createdAt: '2026-01-01T00:00:00Z',
          },
        ],
      }),
    );
    await renderListings();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: /Try again|Retry/i }));
    await waitFor(() => expect(screen.getByText('Winter jacket')).toBeTruthy());
  });
});
