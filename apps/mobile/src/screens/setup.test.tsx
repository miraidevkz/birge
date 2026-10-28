import { afterEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { needsSetup, SETUP_KEY, SetupScreen } from './setup';

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
      newIdempotencyKey: () => '01900000-0000-7000-8000-000000000005',
      fetch: (...args: Parameters<typeof fetch>) => mockFetch(...args),
    }),
    kratos: {},
  };
});

const member = {
  id: '01900000-0000-7000-8000-000000000010',
  lang: 'en',
  verification: 'new',
  roles: ['mom'],
  children: [],
};
const calls: { method: string; path: string; body: unknown }[] = [];

function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
}

function serve() {
  mockFetch.mockImplementation(async (input) => {
    const req = input as Request;
    const path = new URL(req.url).pathname.replace('/v1', '');
    const body = req.method === 'GET' ? undefined : await req.json().catch(() => undefined);
    calls.push({ method: req.method, path, body });
    if (path === '/me' && req.method === 'GET') return json(member);
    if (path === '/me' && req.method === 'PATCH') return json({ ...member, ...(body as object) });
    if (path === '/me/verification-requests') return json({ ...member, verification: 'pending' }, 202);
    if (path === '/reference/districts') return json([]);
    if (path === '/reference/needs') return json([]);
    return json({}, 404);
  });
}

afterEach(() => {
  mockFetch.mockReset();
  calls.length = 0;
});

describe('SetupScreen', () => {
  it('saves her name, goes through the steps, asks for verification and finishes', async () => {
    serve();
    const onDone = jest.fn();
    await render(
      <Providers>
        <SetupScreen onDone={onDone} />
      </Providers>,
    );
    await fireEvent.changeText(await screen.findByTestId('setup-name-input'), 'Aigerim');
    await fireEvent.press(screen.getByTestId('setup-next'));
    await waitFor(() =>
      expect(
        calls.some((c) => c.method === 'PATCH' && (c.body as { displayName?: string })?.displayName === 'Aigerim'),
      ).toBe(true),
    );
    expect(await screen.findByTestId('setup-about')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('setup-next'));
    await fireEvent.press(await screen.findByTestId('setup-verify-request'));
    expect(await screen.findByText('Request sent. A moderator will call you.')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('setup-next'));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(await storage.get(SETUP_KEY)).toBe('1');
  });

  it('skip finishes at once and is remembered', async () => {
    serve();
    await storage.remove(SETUP_KEY);
    const onDone = jest.fn();
    await render(
      <Providers>
        <SetupScreen onDone={onDone} />
      </Providers>,
    );
    await fireEvent.press(await screen.findByTestId('setup-skip'));
    await waitFor(() => expect(onDone).toHaveBeenCalled());
    expect(await needsSetup(undefined)).toBe(false);
  });

  it('is needed only without a name and before it was done here', async () => {
    await storage.remove(SETUP_KEY);
    expect(await needsSetup(undefined)).toBe(true);
    expect(await needsSetup('Dana')).toBe(false);
  });
});
