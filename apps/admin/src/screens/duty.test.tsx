import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { currentWeekRange, DutyScreen } from './duty';

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
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const SHIFT = {
  id: '01900000-0000-7000-8000-0000000000s1',
  staffId: '01900000-0000-7000-8000-0000000000a1',
  role: 'psychologist',
  startsAt: '2026-10-27T09:00:00.000Z',
  endsAt: '2026-10-27T17:00:00.000Z',
  backup: false,
};

const renderScreen = () =>
  render(
    <Providers>
      <DutyScreen />
    </Providers>,
  );

beforeEach(() => {
  mockFetch.mockReset();
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'admin-1', roles: ['admin'] });
});

describe('currentWeekRange', () => {
  it('returns Monday-aligned UTC bounds', () => {
    // Wednesday 2026-10-28
    const week = currentWeekRange(new Date('2026-10-28T12:00:00Z'));
    expect(week.from).toBe('2026-10-26T00:00:00.000Z');
    expect(week.to).toBe('2026-11-02T00:00:00.000Z');
    expect(week.label).toBe('2026-10-26');
  });
});

describe('DutyScreen', () => {
  it('renders the title', async () => {
    mockFetch.mockResolvedValue(json(200, []));
    await renderScreen();
    expect(await screen.findByRole('header', { name: 'Duty roster' })).toBeTruthy();
  });

  it('shows the empty state', async () => {
    mockFetch.mockResolvedValue(json(200, []));
    await renderScreen();
    expect(await screen.findByText('No shifts this week')).toBeTruthy();
  });

  it('shows the error state', async () => {
    mockFetch.mockResolvedValue(json(500, { title: 'server error', code: 'internal' }));
    await renderScreen();
    expect(await screen.findByText(/Something went wrong/)).toBeTruthy();
  });

  it('deletes a shift', async () => {
    mockFetch
      .mockResolvedValueOnce(json(200, [SHIFT]))
      .mockResolvedValueOnce(json(204))
      .mockResolvedValueOnce(json(200, []));
    await renderScreen();
    expect(await screen.findByText('Psychologist')).toBeTruthy();
    await fireEvent.press(screen.getByTestId(`delete-${SHIFT.id}`));
    await waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(3));
    const del = mockFetch.mock.calls[1]![0] as Request;
    expect(del.method).toBe('DELETE');
    expect(del.url).toContain(`/staff/duty-roster/${SHIFT.id}`);
  });
});
