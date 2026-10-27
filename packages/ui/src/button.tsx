import { ActivityIndicator, Pressable, View } from 'react-native';

import { cn } from './cn';
import { useMotionAllowed } from './motion';
import { Text, type TextTone } from './text';
import { useThemeColors, type ColorName } from './theme';

const variants = {
  primary: { box: 'bg-primary', tone: 'onPrimary', spinner: 'primaryForeground' },
  secondary: { box: 'bg-secondary', tone: 'onSecondary', spinner: 'secondaryForeground' },
  destructive: { box: 'bg-destructive', tone: 'onDestructive', spinner: 'destructiveForeground' },
} as const satisfies Record<string, { box: string; tone: TextTone; spinner: ColorName }>;

const sizes = {
  md: 'min-h-touch px-4 py-2',
  sm: 'min-h-touch px-3 py-1',
} as const;

/** ButtonVariant: one primary action per screen (ADR-0014); destructive for SOS and irreversible actions. */
export type ButtonVariant = keyof typeof variants;

/** ButtonProps are the button's contract: content is the title, nothing else. */
export interface ButtonProps {
  /** title is the visible text and the accessibility label. */
  title: string;
  onPress?: () => void;
  variant?: ButtonVariant;
  size?: keyof typeof sizes;
  /** loading keeps the title visible, shows progress and blocks repeated presses. */
  loading?: boolean;
  disabled?: boolean;
  /** accessibilityHint says what happens, when the title alone does not. */
  accessibilityHint?: string;
  /** className adjusts layout (margins, width) only. */
  className?: string;
  testID?: string;
}

/**
 * Button is a pressable action with pressed, disabled and loading states.
 * Pressed feedback is an instant opacity change, not an animation, so it is
 * the same on crisis screens.
 */
export function Button({
  title,
  onPress,
  variant = 'primary',
  size = 'md',
  loading = false,
  disabled = false,
  accessibilityHint,
  className,
  testID,
}: ButtonProps) {
  const colors = useThemeColors();
  const motion = useMotionAllowed();
  const v = variants[variant];
  const inactive = disabled || loading;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={title}
      accessibilityHint={accessibilityHint}
      accessibilityState={{ disabled: inactive, busy: loading }}
      disabled={inactive}
      onPress={onPress}
      testID={testID}
      className={cn(
        'flex-row items-center justify-center gap-2 rounded-md active:opacity-80',
        v.box,
        sizes[size],
        disabled && 'opacity-50',
        className,
      )}
    >
      {/* A spinning indicator is motion; under reduce motion or on crisis
          screens the busy state is announced and the title stays. */}
      {loading && motion ? (
        <ActivityIndicator
          color={colors[v.spinner]}
          accessibilityElementsHidden
          importantForAccessibility="no"
          testID="button-spinner"
        />
      ) : null}
      <View className="shrink">
        <Text variant="label" tone={v.tone} className="text-center">
          {title}
        </Text>
      </View>
    </Pressable>
  );
}
