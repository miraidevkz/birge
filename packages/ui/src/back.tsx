import { createContext, useContext, type ReactNode } from 'react';

/**
 * ScreenBackContext carries the "go back" action of a stacked screen. The
 * app's navigator provides it for every screen above the tabs, so each Screen
 * shows one Back control in its header without the screen asking for it; tab
 * roots, sign-in and onboarding get none.
 */
const ScreenBackContext = createContext<(() => void) | undefined>(undefined);

/** ScreenBackProviderProps: what Back does (usually the router's back). */
export interface ScreenBackProviderProps {
  onBack: () => void;
  children: ReactNode;
}

/** ScreenBackProvider gives the Screens below it a Back control. */
export function ScreenBackProvider({ onBack, children }: ScreenBackProviderProps) {
  return <ScreenBackContext.Provider value={onBack}>{children}</ScreenBackContext.Provider>;
}

/** useScreenBack returns the Back action of the current screen, if it has one. */
export function useScreenBack(): (() => void) | undefined {
  return useContext(ScreenBackContext);
}
