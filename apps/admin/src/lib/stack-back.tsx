// The shared Back of screens stacked above a tab root (the Staff list and the
// staff card under Audit). Screen renders the Back control itself when a
// ScreenBackProvider is above it; this wires it to the router. Opened
// directly (a reload, a pasted URL) there is no history, so Back goes to the
// tab root instead of doing nothing.
import { ScreenBackProvider } from '@birge/ui';
import { router, type Href } from 'expo-router';
import type { ReactNode } from 'react';

/** StackBackProps: where Back goes when there is no history, and the screen. */
export interface StackBackProps {
  fallback: Href;
  children: ReactNode;
}

/** StackBack gives the screen below it the shared Back control. */
export function StackBack({ fallback, children }: StackBackProps) {
  const onBack = () => {
    if (router.canGoBack()) router.back();
    else router.replace(fallback);
  };
  return <ScreenBackProvider onBack={onBack}>{children}</ScreenBackProvider>;
}
