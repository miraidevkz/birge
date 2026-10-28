import type { ReactNode } from 'react';
import { Pressable, View } from 'react-native';

import { cn } from './cn';

/** CardProps: content is children; pass onPress plus accessibilityLabel to make the whole card one button. */
export interface CardProps {
  children: ReactNode;
  onPress?: () => void;
  /** accessibilityLabel is required with onPress: the card is read as one control. */
  accessibilityLabel?: string;
  /** tone "accent" is the warm sand surface for the one thing to do next (ADR-0025); use it once per screen. */
  tone?: 'default' | 'accent';
  className?: string;
  testID?: string;
}

// Soft raised surface (ADR-0025): large radius and a faint shadow; the hairline
// border keeps cards apart from the ground in dark mode, where shadows vanish.
const base = 'gap-3 rounded-xl border border-separator/60 bg-card p-5 shadow-sm';

/** Card groups related content on a raised surface. Use it for a section, not for every row (use ListItem). */
export function Card({ children, onPress, accessibilityLabel, tone = 'default', className, testID }: CardProps) {
  const surface = tone === 'accent' ? 'bg-accent border-transparent' : undefined;
  if (onPress) {
    return (
      <Pressable
        accessibilityRole="button"
        accessibilityLabel={accessibilityLabel}
        onPress={onPress}
        testID={testID}
        className={cn(base, surface, 'active:opacity-80', className)}
      >
        {children}
      </Pressable>
    );
  }
  return (
    <View className={cn(base, surface, className)} testID={testID}>
      {children}
    </View>
  );
}
