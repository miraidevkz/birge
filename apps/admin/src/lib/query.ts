// React Query setup. Retries only what a retry can fix; 401 ends the local
// session; 403 mfa_required clears the token and flags step-up.
import { ApiError, isRetryable } from '@birge/api';
import { focusManager, MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';
import { AppState, Platform } from 'react-native';

import { expireAccessToken, requireMfa } from './auth';

function handleAuthError(error: unknown): void {
  if (!(error instanceof ApiError)) return;
  if (error.status === 401) {
    expireAccessToken();
    return;
  }
  if (error.status === 403 && error.code === 'mfa_required') requireMfa();
}

/** createQueryClient builds the admin query client. */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    queryCache: new QueryCache({ onError: handleAuthError }),
    mutationCache: new MutationCache({ onError: handleAuthError }),
    defaultOptions: {
      queries: {
        staleTime: 60_000,
        retry: (count, error) =>
          error instanceof ApiError && (error.status === 401 || error.code === 'mfa_required')
            ? false
            : isRetryable(error) && count < 2,
      },
      mutations: { retry: false },
    },
  });
}

/** syncFocusWithAppState refetches stale queries when the tab returns. */
export function syncFocusWithAppState(): () => void {
  if (Platform.OS === 'web') return () => undefined;
  const sub = AppState.addEventListener('change', (s) => focusManager.setFocused(s === 'active'));
  return () => sub.remove();
}
