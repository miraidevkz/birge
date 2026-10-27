import { useId } from 'react';
import { TextInput, View, type TextInputProps } from 'react-native';

import { cn } from './cn';
import { Text } from './text';
import { useThemeColors } from './theme';

/** TextFieldProps are TextInput props minus styling, plus a required label. */
export type TextFieldProps = Omit<
  TextInputProps,
  'style' | 'className' | 'placeholderTextColor' | 'accessibilityLabel'
> & {
  /** label is shown above the field and is its accessibility label; placeholders are not labels. */
  label: string;
  /** hint is shown below the field and read after the label. */
  hint?: string;
  /** error replaces the hint, is announced, and marks the field invalid. */
  error?: string;
};

/** TextField is a labelled text input with hint and error text. */
export function TextField({ label, hint, error, editable = true, ...props }: TextFieldProps) {
  const colors = useThemeColors();
  const id = useId();
  return (
    <View className="gap-1">
      <Text variant="label" nativeID={`${id}-label`}>
        {label}
      </Text>
      <TextInput
        accessibilityLabel={label}
        accessibilityHint={error ?? hint}
        aria-invalid={error ? true : undefined}
        aria-labelledby={`${id}-label`}
        editable={editable}
        placeholderTextColor={colors.mutedForeground}
        className={cn(
          'min-h-touch rounded-md border bg-card px-4 py-2 font-sans text-body text-foreground',
          error ? 'border-destructive' : 'border-border',
          !editable && 'opacity-50',
        )}
        {...props}
      />
      {error ? (
        <Text variant="caption" tone="destructive" accessibilityLiveRegion="polite" accessibilityRole="alert">
          {error}
        </Text>
      ) : hint ? (
        <Text variant="caption" tone="muted">
          {hint}
        </Text>
      ) : null}
    </View>
  );
}
