import { describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen, waitFor } from '@testing-library/react-native';
import { SignInError } from '@birge/api';

import { kratos } from '@/lib/api';
import { storage } from '@/lib/storage';
import { Providers } from '@/test-utils';

import { normalizePhone, SignInScreen } from './sign-in';

jest.mock('expo-constants', () => require('@/test-mocks').expoConstants());
jest.mock('@/lib/storage', () => require('@/test-mocks').memoryStorage());
jest.mock('@/lib/api', () => ({
  api: {},
  kratos: { startSignIn: jest.fn(), verifyCode: jest.fn(), signOut: jest.fn() },
}));

const mocked = kratos as unknown as {
  startSignIn: jest.Mock<typeof kratos.startSignIn>;
  verifyCode: jest.Mock<typeof kratos.verifyCode>;
};

const PHONE = '+70000000000';

describe('normalizePhone', () => {
  it('drops spaces, dashes and brackets', () => {
    expect(normalizePhone('+7 (000) 000-00-00')).toBe(PHONE);
  });
});

describe('SignInScreen', () => {
  it('rejects a malformed number without calling the server', async () => {
    await render(
      <Providers>
        <SignInScreen onSignedIn={() => undefined} />
      </Providers>,
    );
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), '8 700 000');
    await fireEvent.press(screen.getByRole('button', { name: 'Send code' }));
    expect(screen.getByRole('alert')).toHaveTextContent('Enter a valid mobile number in international format.');
    expect(mocked.startSignIn).not.toHaveBeenCalled();
  });

  it('sends the code, verifies it and stores the session', async () => {
    mocked.startSignIn.mockResolvedValue({ flow: 'login', flowId: 'f1', phone: PHONE });
    mocked.verifyCode.mockResolvedValue({ token: 'session-token' });
    const onSignedIn = jest.fn();
    await render(
      <Providers>
        <SignInScreen onSignedIn={onSignedIn} />
      </Providers>,
    );
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), '+7 000 000 00 00');
    await fireEvent.press(screen.getByRole('button', { name: 'Send code' }));
    expect(mocked.startSignIn).toHaveBeenCalledWith(PHONE);

    await fireEvent.changeText(await screen.findByLabelText('Code from SMS'), '123456');
    await fireEvent.press(screen.getByRole('button', { name: 'Sign in' }));
    await waitFor(() => expect(onSignedIn).toHaveBeenCalled());
    expect(mocked.verifyCode).toHaveBeenCalledWith({ flow: 'login', flowId: 'f1', phone: PHONE }, '123456');
    expect(await storage.get('birge.session')).toBe('session-token');
  });

  it('shows the text for a wrong code and keeps the code step', async () => {
    mocked.startSignIn.mockResolvedValue({ flow: 'login', flowId: 'f1', phone: PHONE });
    mocked.verifyCode.mockRejectedValue(new SignInError('invalid_code'));
    await render(
      <Providers>
        <SignInScreen onSignedIn={() => undefined} />
      </Providers>,
    );
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), PHONE);
    await fireEvent.press(screen.getByRole('button', { name: 'Send code' }));
    await fireEvent.changeText(await screen.findByLabelText('Code from SMS'), '1111');
    await fireEvent.press(screen.getByRole('button', { name: 'Sign in' }));
    expect(await screen.findByText('The code is not correct. Check the SMS and try again.')).toBeTruthy();
    expect(screen.getByLabelText('Code from SMS')).toBeTruthy();
  });
});
