import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { SpecialistsScreen } from './specialists';

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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000005',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const renderSpecialists = () =>
  render(
    <Providers>
      <SpecialistsScreen />
    </Providers>,
  );

beforeEach(async () => {
  mockFetch.mockReset();
  await storage.set('birge.session', 'session-token');
});

describe('SpecialistsScreen', () => {
  it('renders the specialists title', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderSpecialists();
    expect(await screen.findByRole('header', { name: 'Specialists' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, { items: [] }));
    await renderSpecialists();
    expect(await screen.findByText('No specialists yet')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderSpecialists();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('lists a specialist and retries after an error', async () => {
    mockFetch.mockResolvedValueOnce(json(500, { title: 'server error', code: 'internal' })).mockResolvedValueOnce(
      json(200, {
        items: [
          {
            id: '01900000-0000-7000-8000-0000000000s1',
            name: 'Example Clinic',
            profession: 'speech_therapist',
            districtId: '01900000-0000-7000-8000-0000000000d1',
            needs: ['speech'],
            status: 'active',
            averageRating: 4.5,
            reviewCount: 3,
            latestReviews: [],
          },
        ],
      }),
    );
    await renderSpecialists();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: /Try again|Retry/i }));
    await waitFor(() => expect(screen.getByText('Example Clinic')).toBeTruthy());
  });
});
