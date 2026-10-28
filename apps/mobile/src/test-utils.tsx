// Shared helpers for app tests. Not bundled: nothing in src/app imports it.
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useState, type ReactNode } from 'react';

import { SessionProvider } from '@/lib/session';

/** ProvidersProps: the screen, and optionally a query client and sign-out hook of the test. */
export interface ProvidersProps {
  children: ReactNode;
  client?: QueryClient;
  beforeSignOut?: () => Promise<void>;
}

/** Providers wraps a screen with a fresh query client and the session. */
export function Providers({ children, client, beforeSignOut }: ProvidersProps) {
  const [own] = useState(() => new QueryClient({ defaultOptions: { queries: { retry: false } } }));
  return (
    <QueryClientProvider client={client ?? own}>
      <SessionProvider beforeSignOut={beforeSignOut}>{children}</SessionProvider>
    </QueryClientProvider>
  );
}
