import { describe, expect, it, jest } from '@jest/globals';
import { fireEvent, render, screen } from '@testing-library/react-native';

import { Button } from './button';
import { ChoiceGroup } from './choice-group';
import { List } from './list';
import { ListItem } from './list-item';
import { Screen } from './screen';
import { SosProvider } from './sos';
import { Text } from './text';
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
});

describe('TextField', () => {
  it('is labelled and announces its error', async () => {
    await render(<TextField label="Mobile number" value="" error="Enter a valid mobile number." />);
    expect(screen.getByLabelText('Mobile number')).toBeTruthy();
    expect(screen.getByRole('alert')).toHaveTextContent('Enter a valid mobile number.');
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
