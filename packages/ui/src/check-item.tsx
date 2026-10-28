import { View } from 'react-native';

import { cn } from './cn';
import { PressableBox } from './pressable-box';
import { Text } from './text';

/** CheckItemProps: one option that can be on or off. */
export interface CheckItemProps {
  title: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
  /** radio makes it one of several exclusive options (a district); default is a checkbox. */
  radio?: boolean;
  disabled?: boolean;
  testID?: string;
}

/**
 * CheckItem is a full-width row that toggles (needs of a child) or selects
 * one of several (a district). The whole row is the touch target and is
 * announced as a checkbox or radio with its state. Press feedback comes from
 * PressableBox.
 */
export function CheckItem({ title, checked, onChange, radio = false, disabled = false, testID }: CheckItemProps) {
  return (
    <PressableBox
      accessibilityRole={radio ? 'radio' : 'checkbox'}
      accessibilityState={{ checked, disabled }}
      accessibilityLabel={title}
      disabled={disabled}
      onPress={() => onChange(radio ? true : !checked)}
      testID={testID}
      boxClassName={cn(
        'min-h-touch flex-row items-center gap-3 border-b border-separator py-3',
        disabled && 'opacity-50',
      )}
    >
      <View
        className={cn(
          'h-6 w-6 items-center justify-center border-2',
          radio ? 'rounded-full' : 'rounded-md',
          checked ? 'border-primary bg-primary' : 'border-border bg-card',
        )}
      >
        {checked ? (
          <View className={cn('h-2 w-2 bg-primary-foreground', radio ? 'rounded-full' : 'rounded-sm')} />
        ) : null}
      </View>
      <Text className="flex-1">{title}</Text>
    </PressableBox>
  );
}
