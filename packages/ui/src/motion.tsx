// Motion policy (ADR-0014): motion runs only when the OS "reduce motion"
// setting is off and the screen is not a crisis or SOS screen. Components ask
// useMotionAllowed() and render a static variant otherwise, so screens never
// decide this themselves.
import { createContext, useContext, useSyncExternalStore, type ReactNode } from 'react';
import { AccessibilityInfo } from 'react-native';
import { Easing } from 'react-native-reanimated';

import { tokens } from './theme';

const CrisisContext = createContext(false);

/** NoMotion turns every animation in its subtree off (used by crisis screens). */
export function NoMotion({ children }: { children: ReactNode }) {
  return <CrisisContext.Provider value>{children}</CrisisContext.Provider>;
}

// The OS setting is read asynchronously, so there is a moment after start-up
// when it is not known yet. It counts as "reduce motion" until the answer
// comes: an entering animation that started on a wrong guess could not be
// taken back. One module-level store serves every hook; it listens while at
// least one component uses it and forgets the value when none does, so it
// never reports a stale setting after a long time without subscribers.
let reduceMotion: boolean | undefined;
let subscription: { remove: () => void } | undefined;
// Counts listening periods, so an answer that arrives after everyone
// unsubscribed and someone subscribed again cannot overwrite the new one.
let epoch = 0;
const listeners = new Set<() => void>();

function setReduceMotion(value: boolean) {
  if (reduceMotion === value) return;
  reduceMotion = value;
  for (const l of listeners) l();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) {
    const mine = ++epoch;
    subscription = AccessibilityInfo.addEventListener('reduceMotionChanged', setReduceMotion);
    // A failed query leaves the value unknown: motion stays off.
    AccessibilityInfo.isReduceMotionEnabled()
      .then((v) => {
        if (mine === epoch && listeners.size > 0) setReduceMotion(v);
      })
      .catch(() => undefined);
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      subscription?.remove();
      subscription = undefined;
      reduceMotion = undefined;
    }
  };
}

function snapshot(): boolean {
  return reduceMotion ?? true;
}

/**
 * useReduceMotion reports the OS reduce-motion setting and follows changes.
 * It is true until the setting is known (see above).
 */
export function useReduceMotion(): boolean {
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

/** useMotionAllowed is false under reduce motion and inside NoMotion. */
export function useMotionAllowed(): boolean {
  const crisis = useContext(CrisisContext);
  const reduce = useReduceMotion();
  return !crisis && !reduce;
}

/** useDuration returns the token duration in ms, or 0 when motion is not allowed. */
export function useDuration(speed: keyof typeof tokens.motion = 'base'): number {
  return useMotionAllowed() ? tokens.motion[speed] : 0;
}

/**
 * easeOut is the one curve for UI motion (ADR-0014: timing curves, no spring
 * or bounce): a strong ease-out, so the change is visible at once and settles
 * softly. The built-in Easing.out curves start too slowly for 150-250 ms.
 */
export const easeOut = Easing.bezier(0.23, 1, 0.32, 1);
