import { describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { ROOT_ROUTES } from '@/lib/routes';

import { AccountDeletedScreen } from './account-deleted';

jest.mock('expo-router', () => ({
  router: { push: jest.fn(), replace: jest.fn(), back: jest.fn() },
}));

describe('AccountDeletedScreen', () => {
  it('says what happens next and when, without any request', async () => {
    await render(<AccountDeletedScreen />);
    expect(screen.getByRole('header', { name: 'Your request is received' })).toBeTruthy();
    expect(screen.getByText(/will be deleted within 24 hours/)).toBeTruthy();
    expect(screen.getByRole('header', { name: 'What happens next' })).toBeTruthy();
    expect(screen.getByText(/cannot be read or restored/)).toBeTruthy();
  });

  it('has one action, back to the start', async () => {
    const { router } = require('expo-router');
    await render(<AccountDeletedScreen />);
    await fireEvent.press(screen.getByRole('button', { name: 'Back to the start' }));
    expect(router.replace).toHaveBeenCalledWith('/');
  });

  it('is a root of the stack: no Back into screens of the deleted account', () => {
    expect(ROOT_ROUTES.has('account-deleted')).toBe(true);
  });
});
