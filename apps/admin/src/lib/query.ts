// React Query setup. Retries only what a retry can fix (never a 501: the
// server does not serve that route yet); 401 ends the local session; 403
// mfa_required clears the token and flags step-up.
import { ApiError, isRetryable } from '@birge/api';
import { focusManager, MutationCache, QueryCache, QueryClient } from '@tanstack/react-query';
import { AppState, Platform } from 'react-native';

import { expireAccessToken, requireMfa } from './auth';
import { isNotImplemented } from './not-implemented';

function handleAuthError(error: unknown): void {
  if (!(error instanceof ApiError)) return;
  if (error.status === 401) {
    expireAccessToken();
    return;
  }
  if (error.status === 403 && error.code === 'mfa_required') requireMfa();
}

function shouldRetry(count: number, error: unknown): boolean {
  if (error instanceof ApiError && (error.status === 401 || error.code === 'mfa_required')) return false;
  if (isNotImplemented(error)) return false;
  return isRetryable(error) && count < 2;
}

/** createQueryClient builds the admin query client. */
export function createQueryClient(): QueryClient {
  return new QueryClient({
    queryCache: new QueryCache({ onError: handleAuthError }),
    mutationCache: new MutationCache({ onError: handleAuthError }),
    defaultOptions: {
      queries: {
        staleTime: 60_000,
        retry: shouldRetry,
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
