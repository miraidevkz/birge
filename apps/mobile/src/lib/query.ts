// React Query setup. Retries only what a retry can fix (isRetryable), and a
// 401 ends the local session so the member is sent to sign-in.
import { ApiError, isRetryable } from '@birge/api';
import { focusManager, QueryCache, QueryClient } from '@tanstack/react-query';
import { AppState, Platform } from 'react-native';

import { expireSession } from './session';

/** createQueryClient builds the app's query client. */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    queryCache: new QueryCache({
      onError(error) {
        if (error instanceof ApiError && error.status === 401) expireSession();
      },
    }),
    defaultOptions: {
      queries: {
        staleTime: 60_000,
        retry: (count, error) =>
          error instanceof ApiError && error.status === 401 ? false : isRetryable(error) && count < 2,
      },
      // Mutations are never retried blindly; crisis requests go through the outbox.
      mutations: { retry: false },
    },
  });
}

/** syncFocusWithAppState refetches stale queries when the app returns to the foreground. */
export function syncFocusWithAppState(): () => void {
  if (Platform.OS === 'web') return () => undefined;
  const sub = AppState.addEventListener('change', (s) => focusManager.setFocused(s === 'active'));
  return () => sub.remove();
}
