// Press feedback for every pressable component (ADR-0014): a slight scale on
// the UI thread when motion is allowed, the instant opacity change otherwise.
import type { ReactNode } from 'react';
import { Pressable, StyleSheet, View, type GestureResponderEvent, type PressableProps } from 'react-native';
import Animated, { useAnimatedStyle, useSharedValue, withTiming } from 'react-native-reanimated';

import { cn } from './cn';
import { easeOut, useMotionAllowed } from './motion';
import { tokens } from './theme';

// Feedback strengths, not layout values: "barely smaller" and the opacity
// the components had as `active:opacity-80` before.
const PRESSED_SCALE = 0.98;
const PRESSED_OPACITY = 0.8;

/**
 * usePressFeedback returns the animated style and the press handlers for one
 * pressable. With motion it scales to PRESSED_SCALE within the fast token on
 * the UI thread; under reduce motion or in NoMotion it switches opacity
 * instantly, which is feedback, not motion, and the same on crisis screens.
 * `animated` reports which of the two is in effect (for tests).
 */
export function usePressFeedback() {
  const motion = useMotionAllowed();
  const pressed = useSharedValue(0);
  const style = useAnimatedStyle(
    () =>
      motion
        ? { opacity: 1, transform: [{ scale: 1 - (1 - PRESSED_SCALE) * pressed.get() }] }
        : { opacity: 1 - (1 - PRESSED_OPACITY) * pressed.get(), transform: [{ scale: 1 }] },
    [motion],
  );
  const to = (value: number) =>
    pressed.set(motion ? withTiming(value, { duration: tokens.motion.fast, easing: easeOut }) : value);
  return { style, animated: motion, onPressIn: () => to(1), onPressOut: () => to(0) };
}

/** PressableBoxProps: a Pressable whose visual box gets the press feedback. */
export interface PressableBoxProps extends Omit<PressableProps, 'children' | 'style' | 'className'> {
  children: ReactNode;
  /** className lays out the touch target in its parent (margins, width). */
  className?: string;
  /** boxClassName styles the visible box: surface, padding, direction, gaps. */
  boxClassName?: string;
}

/**
 * PressableBox is the one pressable surface of packages/ui (Button, Card,
 * ListItem, CheckItem, ChoiceGroup). The Pressable keeps the role, state and
 * label; the Reanimated view inside carries only the feedback, and the plain
 * view inside that carries the NativeWind classes, so the structure is the
 * same with and without motion.
 */
export function PressableBox({ children, className, boxClassName, onPressIn, onPressOut, ...rest }: PressableBoxProps) {
  const feedback = usePressFeedback();
  return (
    <Pressable
      {...rest}
      className={className}
      onPressIn={(e: GestureResponderEvent) => {
        feedback.onPressIn();
        onPressIn?.(e);
      }}
      onPressOut={(e: GestureResponderEvent) => {
        feedback.onPressOut();
        onPressOut?.(e);
      }}
    >
      <Animated.View style={[styles.fill, feedback.style]}>
        <View className={cn('grow', boxClassName)}>{children}</View>
      </Animated.View>
    </Pressable>
  );
}

// The box fills the touch target when a parent stretches it (a chip row, a
// full-width button), as the Pressable itself did before.
const styles = StyleSheet.create({ fill: { flexGrow: 1 } });
