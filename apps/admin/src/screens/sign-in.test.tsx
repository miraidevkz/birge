import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react-native';

import { clearAccessToken, getAccessToken, requireMfa, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { SignInScreen } from './sign-in';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
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

const mockSignIn = jest.fn<() => Promise<{ accessToken: string; profile: { subject: string; roles: string[] } }>>();
jest.mock('@/lib/auth', () => {
  const actual = jest.requireActual<typeof import('@/lib/auth')>('@/lib/auth');
  return {
    ...actual,
    signInWithOidc: () => mockSignIn(),
  };
});

describe('SignInScreen', () => {
  beforeEach(() => {
    clearAccessToken();
    mockSignIn.mockReset();
  });

  it('renders the staff sign-in title and button', async () => {
    await render(
      <Providers>
        <SignInScreen onSignedIn={() => undefined} />
      </Providers>,
    );
    expect(screen.getByRole('header', { name: 'Staff sign-in' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeTruthy();
  });

  it('signs in through OIDC and keeps the token in memory', async () => {
    mockSignIn.mockResolvedValue({
      accessToken: 'access-token',
      profile: { subject: 'staff-1', roles: ['psychologist'] },
    });
    // session.signIn calls the real setAccessToken via the mocked oidc return...
    // The SessionProvider uses the mocked signInWithOidc; after success it stores the snapshot.
    const onSignedIn = jest.fn();
    // Patch: SessionProvider's signIn calls signInWithOidc then setAccessToken.
    // Our mock returns the snapshot; SessionProvider will call setAccessToken.
    await render(
      <Providers>
        <SignInScreen onSignedIn={onSignedIn} />
      </Providers>,
    );
    await fireEvent.press(screen.getByTestId('staff-sign-in'));
    await waitFor(() => expect(onSignedIn).toHaveBeenCalled());
    expect(mockSignIn).toHaveBeenCalled();
    expect(getAccessToken()).toBe('access-token');
  });

  it('shows the MFA message when mfa_required was raised', async () => {
    setAccessToken('tok', { subject: 's', roles: [] });
    await render(
      <Providers>
        <SignInScreen onSignedIn={() => undefined} />
      </Providers>,
    );
    // Trigger MFA after mount so the provider handler is registered.
    await act(async () => {
      requireMfa();
    });
    expect(await screen.findByRole('header', { name: 'Second factor required' })).toBeTruthy();
    expect(screen.getByText(/Your organisation requires a second factor/)).toBeTruthy();
    expect(screen.getByTestId('mfa-sign-in')).toBeTruthy();
  });
});
