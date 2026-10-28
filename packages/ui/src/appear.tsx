// First-appearance motion (ADR-0014, ADR-0025): content fades in and rises a
// few px when it mounts, on the UI thread (Reanimated layout animation), with
// a timing curve from the motion tokens. It never runs again for the same
// mounted view, never under reduce motion and never inside NoMotion.
import { createContext, useContext, useState, type ReactNode } from 'react';
import { View } from 'react-native';
import Animated, { FadeIn, FadeInDown, type EntryOrExitLayoutType } from 'react-native-reanimated';

import { easeOut, useMotionAllowed } from './motion';
import { tokens } from './theme';

// How far content rises while it fades in: "a few px", one grid step.
const RISE = tokens.spacing[2];
// Only the first rows of a list animate; each starts a fraction of the fast
// token after the one before, so the last starts one fast step after the first.
const ANIMATED_ROWS = 6;
const STAGGERED_ROWS = ANIMATED_ROWS - 1;
const STAGGER_STEP = tokens.motion.fast / STAGGERED_ROWS;
// Rows mounted later than this after the first row are scroll or recycling
// (FlashList creates cells while she scrolls) and appear without motion.
const FIRST_APPEARANCE_WINDOW = tokens.motion.slow * 2;

/**
 * AppearScope is what a List gives its rows: claim(index) is asked once per
 * mounted row and says whether this row is part of the list's first
 * appearance. The default scope (no List) lets every Appear animate.
 */
interface AppearScope {
  claim?: (index: number) => boolean;
  /** inside is true under another Appear: its motion already covers this content. */
  inside: boolean;
}

const AppearContext = createContext<AppearScope>({ inside: false });

/**
 * AppearListScope gives every Appear in its subtree one shared first-appearance
 * window: the first ANIMATED_ROWS rows animate, staggered,
 * while the window is open; nothing animates after it closes.
 */
export function AppearListScope({ children }: { children: ReactNode }) {
  // A closure over a local variable, not a ref: claim runs in a useState
  // initializer of each row, outside this component's render.
  const [scope] = useState<AppearScope>(() => {
    let firstAt: number | undefined;
    return {
      inside: false,
      claim(index) {
        if (index >= ANIMATED_ROWS) return false;
        const now = Date.now();
        firstAt ??= now;
        return now - firstAt <= FIRST_APPEARANCE_WINDOW;
      },
    };
  });
  return <AppearContext.Provider value={scope}>{children}</AppearContext.Provider>;
}

/** AppearProps: the content and how it enters. */
export interface AppearProps {
  children: ReactNode;
  /** index is the row's position in a List; it sets the stagger. */
  index?: number;
  /** rise adds the small upward movement; off for a plain fade (a step change). */
  rise?: boolean;
  /** className styles the content view (layout and surface). */
  className?: string;
  testID?: string;
}

/**
 * useAppearAnimation decides once, at mount, how this content enters:
 * undefined means it appears without motion. Exported for tests.
 */
export function useAppearAnimation(index = 0, rise = true): EntryOrExitLayoutType | undefined {
  const motion = useMotionAllowed();
  const scope = useContext(AppearContext);
  // Decided once per mount: a re-render (new data, a changed setting) never
  // replays the entrance, and a recycled FlashList cell keeps its view.
  const [entering] = useState(() => {
    if (!motion || scope.inside) return undefined;
    if (scope.claim && !scope.claim(index)) return undefined;
    const delay = Math.min(index, STAGGERED_ROWS) * STAGGER_STEP;
    const duration = tokens.motion.base;
    if (!rise) return FadeIn.duration(duration).delay(delay).easing(easeOut);
    return FadeInDown.duration(duration)
      .delay(delay)
      .easing(easeOut)
      .withInitialValues({ opacity: 0, transform: [{ translateY: RISE }] });
  });
  return entering;
}

const nested: AppearScope = { inside: true };

/**
 * Appear wraps content that should fade in when it first appears: Card does
 * it for itself and List for its rows. Content nested in an Appear does not
 * animate again on its own.
 */
export function Appear({ children, index, rise = true, className, testID }: AppearProps) {
  const entering = useAppearAnimation(index, rise);
  // The Reanimated view carries only the motion; NativeWind classes sit on
  // the plain view inside it (as in Sheet).
  return (
    <Animated.View entering={entering}>
      <AppearContext.Provider value={nested}>
        <View className={className} testID={testID}>
          {children}
        </View>
      </AppearContext.Provider>
    </Animated.View>
  );
}
