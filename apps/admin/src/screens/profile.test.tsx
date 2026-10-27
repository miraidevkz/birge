import { beforeEach, describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { router } from 'expo-router';

import { clearAccessToken, getAccessToken, setAccessToken } from '@/lib/auth';
import { Providers } from '@/test-utils';

import { ProfileScreen } from './profile';

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
jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
}));

const renderScreen = () =>
  render(
    <Providers>
      <ProfileScreen />
    </Providers>,
  );

beforeEach(() => {
  clearAccessToken();
  setAccessToken('staff-token', { subject: 'staff-subject-1', roles: ['psychologist', 'moderator'] });
  (router.replace as jest.Mock).mockReset();
});

describe('ProfileScreen', () => {
  it('renders subject and roles', async () => {
    await renderScreen();
    expect(screen.getByRole('header', { name: 'Profile' })).toBeTruthy();
    expect(screen.getByTestId('staff-subject')).toHaveTextContent('staff-subject-1');
    expect(screen.getByTestId('staff-roles')).toHaveTextContent('psychologist, moderator');
  });

  it('shows a placeholder when roles are missing', async () => {
    clearAccessToken();
    setAccessToken('staff-token', { subject: 'alone', roles: [] });
    await renderScreen();
    expect(screen.getByTestId('staff-roles')).toHaveTextContent('No roles in the token');
  });

  it('signs out and clears the in-memory token', async () => {
    await renderScreen();
    await fireEvent.press(screen.getByTestId('staff-sign-out'));
    await waitFor(() => expect(router.replace).toHaveBeenCalledWith('/sign-in'));
    expect(getAccessToken()).toBeNull();
  });
});
