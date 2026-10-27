// Gate for stack routes that require a member session (chat, listings, specialists).
import { Redirect } from 'expo-router';
import type { ReactNode } from 'react';

import { useSession } from '@/lib/session';

/** SignedIn renders children only after hydration when a session exists. */
export function SignedIn({ children }: { children: ReactNode }) {
  const { hydrated, signedIn } = useSession();
  if (!hydrated) return null;
  if (!signedIn) return <Redirect href="/sign-in" />;
  return children;
}
