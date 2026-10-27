import type { ReactNode } from 'react';
import { Pressable, View } from 'react-native';

import { cn } from './cn';

/** CardProps: content is children; pass onPress plus accessibilityLabel to make the whole card one button. */
export interface CardProps {
  children: ReactNode;
  onPress?: () => void;
  /** accessibilityLabel is required with onPress: the card is read as one control. */
  accessibilityLabel?: string;
  className?: string;
  testID?: string;
}

const base = 'gap-2 rounded-lg border border-separator bg-card p-4';

/** Card groups related content on a raised surface. Use it for a section, not for every row (use ListItem). */
export function Card({ children, onPress, accessibilityLabel, className, testID }: CardProps) {
  if (onPress) {
    return (
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={accessibilityLabel}
        onPress={onPress}
        testID={testID}
        className={cn(base, 'active:opacity-80', className)}
      >
        {children}
      </Pressable>
    );
  }
  return (
    <View className={cn(base, className)} testID={testID}>
      {children}
    </View>
  );
}
