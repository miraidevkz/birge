import { describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { initialsOf, tintOf } from './avatar';
import { Button } from './button';
import { Illustration, type IllustrationName } from './illustration';
import { ChoiceGroup } from './choice-group';
import { List } from './list';
import { ListItem } from './list-item';
import { MessageBubble } from './message-bubble';
import { Screen } from './screen';
import { SosProvider } from './sos';
import { Text } from './text';
import { PhoneField } from './phone-field';
import { TextField } from './text-field';

describe('Button', () => {
  it('is a labelled button that calls onPress', async () => {
    const onPress = jest.fn();
    await render(<Button title="Send code" onPress={onPress} />);
    await fireEvent.press(screen.getByRole('button', { name: 'Send code' }));
    expect(onPress).toHaveBeenCalledTimes(1);
  });

  it('blocks presses and reports busy while loading, keeping its label', async () => {
    const onPress = jest.fn();
    await render(<Button title="Send code" onPress={onPress} loading />);
    const button = screen.getByRole('button', { name: 'Send code' });
    expect(button).toBeBusy();
    expect(button).toBeDisabled();
    expect(screen.queryAllByTestId('button-spinner', { includeHiddenElements: true })).toHaveLength(1);
    await fireEvent.press(button);
    expect(onPress).not.toHaveBeenCalled();
  });
});

describe('Screen', () => {
  it('shows the SOS entry on every default screen', async () => {
    const onSos = jest.fn();
    await render(
      <SosProvider onPress={onSos}>
        <Screen title="Home">
          <Text>content</Text>
        </Screen>
      </SosProvider>,
    );
    expect(screen.getByRole('header', { name: 'Home' })).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'SOS' }));
    expect(onSos).toHaveBeenCalled();
  });

  it('crisis variant: no SOS entry and no animated spinner inside', async () => {
    await render(
      <SosProvider onPress={() => undefined}>
        <Screen title="Help now" variant="crisis">
          <Button title="Contact a person now" loading />
        </Screen>
      </SosProvider>,
    );
    expect(screen.queryByTestId('sos-entry')).toBeNull();
    expect(screen.queryAllByTestId('button-spinner', { includeHiddenElements: true })).toHaveLength(0);
    expect(screen.getByRole('button', { name: 'Contact a person now' })).toBeBusy();
  });

  it('form layout: title, body and footer stay in one centred group', async () => {
    await render(
      <SosProvider onPress={() => undefined}>
        <Screen title="Sign in" layout="form" footer={<Button title="Send code" onPress={() => undefined} />}>
          <Text>Enter your mobile number.</Text>
        </Screen>
      </SosProvider>,
    );
    expect(screen.getByRole('header', { name: 'Sign in' })).toBeTruthy();
    expect(screen.getByText('Enter your mobile number.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Send code' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'SOS' })).toBeTruthy();
  });
});

describe('TextField', () => {
  it('is labelled and announces its error', async () => {
    await render(<TextField label="Mobile number" value="" error="Enter a valid mobile number." />);
    expect(screen.getByLabelText('Mobile number')).toBeTruthy();
    expect(screen.getByRole('alert')).toHaveTextContent('Enter a valid mobile number.');
  });
});

describe('PhoneField', () => {
  it('is labelled, formats while typing, and reports E.164', async () => {
    const onChangePhone = jest.fn();
    await render(<PhoneField label="Mobile number" value="" onChangePhone={onChangePhone} />);
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), '87000000000');
    expect(onChangePhone).toHaveBeenCalledWith('+77000000000');
  });

  it('turns a leading 8 into +7 even when typing outruns the render', async () => {
    // The native text after the second key can be "87" while the field
    // already shows "+7": the inserted '+' must not count as the member's.
    const onChangePhone = jest.fn();
    const { rerender } = await render(<PhoneField label="Mobile number" value="" onChangePhone={onChangePhone} />);
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), '8');
    expect(onChangePhone).toHaveBeenLastCalledWith('+7');
    await rerender(<PhoneField label="Mobile number" value="+7" onChangePhone={onChangePhone} />);
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), '87');
    expect(onChangePhone).toHaveBeenLastCalledWith('+77');
  });

  it("keeps a '+' the member typed first, so a foreign 8x code stays", async () => {
    const onChangePhone = jest.fn();
    const { rerender } = await render(<PhoneField label="Mobile number" value="" onChangePhone={onChangePhone} />);
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), '+');
    await rerender(<PhoneField label="Mobile number" value="+" onChangePhone={onChangePhone} />);
    await fireEvent.changeText(screen.getByLabelText('Mobile number'), '+86');
    expect(onChangePhone).toHaveBeenLastCalledWith('+86');
  });

  it('shows the formatted value from an E.164 prop', async () => {
    await render(<PhoneField label="Mobile number" value="+77000000000" onChangePhone={() => undefined} />);
    expect(screen.getByLabelText('Mobile number')).toHaveDisplayValue('+7 (700) 000-00-00');
  });
});

describe('ChoiceGroup', () => {
  it('marks the selected option and reports a change', async () => {
    const onChange = jest.fn();
    await render(
      <ChoiceGroup
        label="Language"
        value="en"
        onChange={onChange}
        options={[
          { value: 'kk', label: 'Kazakh' },
          { value: 'en', label: 'English' },
        ]}
        testID="lang"
      />,
    );
    expect(screen.getByRole('radio', { name: 'English' })).toBeSelected();
    await fireEvent.press(screen.getByRole('radio', { name: 'Kazakh' }));
    expect(onChange).toHaveBeenCalledWith('kk');
  });
});

describe('List', () => {
  const render_ = (props: Partial<Parameters<typeof List<string>>[0]>) =>
    render(
      <List<string>
        data={undefined}
        renderItem={({ item }) => <ListItem title={item} />}
        keyExtractor={(s) => s}
        empty={{ title: 'Nothing new' }}
        {...props}
      />,
    );

  it('shows loading, not empty, during the first load', async () => {
    await render_({ loading: true });
    expect(screen.getByLabelText('Loading…')).toBeTruthy();
    expect(screen.queryByText('Nothing new')).toBeNull();
  });

  it('shows the error with retry when the first load failed', async () => {
    const onRetry = jest.fn();
    await render_({ error: new Error('x'), onRetry });
    await fireEvent.press(screen.getByRole('button', { name: 'Try again' }));
    expect(onRetry).toHaveBeenCalled();
  });

  it('shows the empty state for an empty result', async () => {
    await render_({ data: [] });
    expect(screen.getByText('Nothing new')).toBeTruthy();
  });
});

describe('Avatar', () => {
  it('shows up to two initials and keeps a stable tint per name', async () => {
    expect(initialsOf('Айгерим Сапарова')).toBe('АС');
    expect(initialsOf('  dana ')).toBe('D');
    expect(initialsOf('')).toBe('·');
    expect(tintOf('Dana')).toBe(tintOf('Dana'));
  });
});

describe('MessageBubble', () => {
  it('shows the author only for other people and is a button when it can be reported', async () => {
    const onPress = jest.fn();
    await render(
      <MessageBubble
        text="Hello"
        author="Dana"
        mine={false}
        onPress={onPress}
        accessibilityLabel="Dana: Hello. Report"
      />,
    );
    expect(screen.getByText('Dana')).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Dana: Hello. Report' }));
    expect(onPress).toHaveBeenCalled();
    await render(<MessageBubble text="Mine" author="Me" mine />);
    expect(screen.queryByText('Me')).toBeNull();
  });
});

describe('Illustration', () => {
  const names: IllustrationName[] = ['welcome', 'inside', 'privacy', 'done'];

  it.each(names)('%s renders and is hidden from assistive technologies', async (name) => {
    await render(<Illustration name={name} size="sm" />);
    const box = screen.getByTestId(`illustration-${name}`, { includeHiddenElements: true });
    expect(box).toBeTruthy();
    // Default queries skip hidden elements: decorative means not announced.
    expect(screen.queryByTestId(`illustration-${name}`)).toBeNull();
  });
});
