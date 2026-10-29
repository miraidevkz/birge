import { afterEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { Providers } from '@/test-utils';

import { AboutMeScreen } from './about-me';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/platform-crypto', () => require('@/test-mocks').platformCrypto());

const mockFetch = jest.fn<typeof fetch>();
jest.mock('@/lib/api', () => {
  const { createApiClient } = require('@birge/api');
  return {
    api: createApiClient({
      baseUrl: 'https://api.example.invalid/v1',
      appVersion: '0.1.0',
      platform: 'android',
      session: async () => 'session-token',
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000004',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const DISTRICT = '01900000-0000-7000-8000-0000000000d1';
const member = {
  id: '01900000-0000-7000-8000-000000000010',
  lang: 'en',
  verification: 'new',
  roles: ['mom'],
  children: [],
};

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

const calls: { method: string; path: string; body: unknown }[] = [];
function serve(me: Record<string, unknown> = member) {
  mockFetch.mockImplementation(async (input) => {
    const req = input as Request;
    const path = new URL(req.url).pathname.replace('/v1', '');
    const body = req.method === 'GET' ? undefined : await req.json().catch(() => undefined);
    calls.push({ method: req.method, path, body });
    if (path === '/me' && req.method === 'GET') return json(me);
    if (path === '/me' && req.method === 'PATCH') return json({ ...me, districtId: DISTRICT });
    if (path === '/reference/districts')
      return json([
        {
          id: '01900000-0000-7000-8000-0000000000c1',
          code: 'demo_city',
          districts: [{ id: DISTRICT, code: 'demo_district_a' }],
        },
      ]);
    if (path === '/reference/needs') return json(['autism', 'hearing']);
    if (path === '/me/children') return json({ id: '01900000-0000-7000-8000-0000000000e1', ...(body as object) }, 201);
    return json({}, 404);
  });
}

const renderAbout = () =>
  render(
    <Providers>
      <AboutMeScreen />
    </Providers>,
  );

afterEach(() => {
  mockFetch.mockReset();
  calls.length = 0;
});

describe('AboutMeScreen', () => {
  it('shows districts and needs with their labels', async () => {
    serve();
    await renderAbout();
    await fireEvent.press(await screen.findByTestId('about-district-select'));
    expect(await screen.findByText('Demo district A')).toBeTruthy();
    expect(await screen.findByText('Autism')).toBeTruthy();
    expect(screen.getByText('Your children will appear here once you add them.')).toBeTruthy();
  });

  it('saves the district she picks', async () => {
    serve();
    await renderAbout();
    await fireEvent.press(await screen.findByTestId('about-district-select'));
    await fireEvent.press(await screen.findByTestId(`about-district-select-${DISTRICT}`));
    await waitFor(() =>
      expect(
        calls.some((c) => c.method === 'PATCH' && (c.body as { districtId?: string })?.districtId === DISTRICT),
      ).toBe(true),
    );
    expect(await screen.findByText('District saved.')).toBeTruthy();
  });

  it('adds a child with needs and rejects a bad year without calling the server', async () => {
    serve();
    await renderAbout();
    await fireEvent.changeText(await screen.findByTestId('about-child-year'), '19');
    await fireEvent.press(screen.getByTestId('about-child-save'));
    expect(screen.getByText('Enter the year of birth as four digits.')).toBeTruthy();
    expect(calls.some((c) => c.path === '/me/children')).toBe(false);
    await fireEvent.changeText(screen.getByTestId('about-child-year'), '2019');
    await fireEvent.press(await screen.findByTestId('about-need-autism'));
    await fireEvent.press(screen.getByTestId('about-child-save'));
    await waitFor(() =>
      expect(calls.find((c) => c.path === '/me/children')?.body).toEqual({ birthYear: 2019, needs: ['autism'] }),
    );
  });

  it('shows an error state when the profile cannot load', async () => {
    mockFetch.mockImplementation(async () => json({ code: 'unavailable' }, 503));
    await renderAbout();
    expect(await screen.findByRole('button', { name: /try again/i })).toBeTruthy();
  });
});
