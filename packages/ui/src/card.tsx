import type { ReactNode } from 'react';
import { Appear } from './appear';
import { cn } from './cn';
import { PressableBox } from './pressable-box';

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

/**
 * Card groups related content on a raised surface. Use it for a section, not
 * for every row (use ListItem). It fades in when it first appears (Appear)
 * and, with onPress, gives press feedback (PressableBox); both are static
 * under reduce motion and on crisis screens.
 */
export function Card({ children, onPress, accessibilityLabel, tone = 'default', className, testID }: CardProps) {
  const surface = tone === 'accent' ? 'bg-accent border-transparent' : undefined;
  if (onPress) {
    return (
      <Appear>
        <PressableBox
          accessibilityRole="button"
          accessibilityLabel={accessibilityLabel}
          onPress={onPress}
          testID={testID}
          boxClassName={cn(base, surface, className)}
        >
          {children}
        </PressableBox>
      </Appear>
    );
  }
  return (
    <Appear className={cn(base, surface, className)} testID={testID}>
      {children}
    </Appear>
  );
}
