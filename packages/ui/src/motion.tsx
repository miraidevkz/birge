// Motion policy (ADR-0014): motion runs only when the OS "reduce motion"
// setting is off and the screen is not a crisis or SOS screen. Components ask
// useMotionAllowed() and render a static variant otherwise, so screens never
// decide this themselves.
import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';
import { AccessibilityInfo } from 'react-native';

import { tokens } from './theme';

const CrisisContext = createContext(false);

/** NoMotion turns every animation in its subtree off (used by crisis screens). */
export function NoMotion({ children }: { children: ReactNode }) {
  return <CrisisContext.Provider value>{children}</CrisisContext.Provider>;
}

/** useReduceMotion reports the OS reduce-motion setting and follows changes. */
export function useReduceMotion(): boolean {
  const [reduce, setReduce] = useState(false);
  useEffect(() => {
    let active = true;
    AccessibilityInfo.isReduceMotionEnabled()
      .then((v) => {
        if (active) setReduce(v);
      })
      .catch(() => undefined);
    const sub = AccessibilityInfo.addEventListener('reduceMotionChanged', setReduce);
    return () => {
      active = false;
      sub.remove();
    };
  }, []);
  return reduce;
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
