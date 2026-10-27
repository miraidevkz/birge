import { Pressable, View } from 'react-native';

import { cn } from './cn';
import { Text } from './text';

/** ChoiceOption is one exclusive option in a ChoiceGroup. */
export interface ChoiceOption<T extends string = string> {
  value: T;
  label: string;
}

/** ChoiceGroupProps: a labelled set of mutually exclusive options. */
export interface ChoiceGroupProps<T extends string = string> {
  /** label names the group for sighted users and as the accessibility label. */
  label: string;
  options: readonly ChoiceOption<T>[];
  value: T;
  onChange: (value: T) => void;
  disabled?: boolean;
  testID?: string;
}

/**
 * ChoiceGroup is a row of exclusive choices (language, knowledge kind, chat
 * scope). Selected state uses the primary fill so the screen can still keep
 * one primary Button for its main action.
 */
export function ChoiceGroup<T extends string>({
  label,
  options,
  value,
  onChange,
  disabled = false,
  testID,
}: ChoiceGroupProps<T>) {
  return (
    <View className="gap-2" accessibilityLabel={label} testID={testID}>
      <Text variant="label">{label}</Text>
      <View className="flex-row flex-wrap gap-2" accessibilityRole="radiogroup">
        {options.map((opt) => {
          const selected = opt.value === value;
          return (
            <Pressable
              key={opt.value}
              accessibilityRole="radio"
              accessibilityLabel={opt.label}
              accessibilityState={{ selected, disabled }}
              disabled={disabled}
              onPress={() => onChange(opt.value)}
              testID={testID ? `${testID}-${opt.value}` : undefined}
              className={cn(
                'min-h-touch items-center justify-center rounded-md border px-3 py-1 active:opacity-80',
                selected ? 'border-primary bg-primary' : 'border-border bg-secondary',
                disabled && 'opacity-50',
              )}
            >
              <Text variant="label" tone={selected ? 'onPrimary' : 'onSecondary'}>
                {opt.label}
              </Text>
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}
