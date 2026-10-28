// The request side of the pages an external trusted contact opens from an
// SMS link (`/plan#<token>`, `/sos-link#<token>`). Both pages read the token
// once, post it without a session through `publicApi` and show one of the
// same few states; this hook is that shared part, so the two cannot drift
// apart in how they treat the token.
//
// The token lives only in a ref for as long as a retry may need it: it is
// not stored, not logged and not part of a React Query key (the request is
// a plain call, not a cached query), so nothing can persist or expose it.
import { ApiError } from '@birge/api';
import { useCallback, useEffect, useRef, useState } from 'react';

import { takeLinkToken } from './link-token';

/** LinkViewState is what a link page shows. */
export type LinkViewState<T> =
  { kind: 'loading' } | { kind: 'ok'; data: T } | { kind: 'gone' } | { kind: 'busy' } | { kind: 'offline' };

/**
 * linkOutcome maps a failed request to what the contact sees. Unknown,
 * expired and revoked links (and for an SOS a closed request) all come back
 * as the same 404 (the server cannot tell them apart on purpose), and so
 * does anything else the server refuses: `gone`. Only a lost connection or
 * a server failure is worth a retry.
 */
export function linkOutcome(err: unknown): LinkViewState<never> {
  if (err instanceof ApiError) {
    if (err.status === 429) return { kind: 'busy' };
    if (err.status >= 500) return { kind: 'offline' };
    return { kind: 'gone' };
  }
  return { kind: 'offline' };
}

/** LinkView is what useLinkView returns: the state and a retry for `offline`. */
export interface LinkView<T> {
  state: LinkViewState<T>;
  retry: () => void;
}

/**
 * useLinkView takes the token from the URL fragment once per page, calls
 * view with it and returns the state and a retry for the `offline` state.
 * A missing or malformed token is `gone` without a request. view should be
 * a module-level function, so retry always calls the same request.
 */
export function useLinkView<T>(view: (token: string) => Promise<T>): LinkView<T> {
  const [state, setState] = useState<LinkViewState<T>>({ kind: 'loading' });
  const token = useRef<string | null>(null);
  const taken = useRef(false);

  const load = useCallback(async () => {
    const value = token.current;
    if (!value) {
      setState({ kind: 'gone' });
      return;
    }
    setState({ kind: 'loading' });
    try {
      const data = await view(value);
      setState({ kind: 'ok', data });
    } catch (err) {
      const next = linkOutcome(err);
      setState(next);
      // Only a retry needs the token; otherwise let it go.
      if (next.kind !== 'offline') token.current = null;
      return;
    }
    token.current = null;
  }, [view]);

  useEffect(() => {
    // Once per page: the fragment is gone after the first read, and a second
    // effect run (React strict mode) must not turn a good link into "gone".
    if (taken.current) return;
    taken.current = true;
    token.current = takeLinkToken();
    void load();
  }, [load]);

  const retry = useCallback(() => void load(), [load]);
  return { state, retry };
}

/** useDocumentTitle sets the browser tab title (a neutral one, not the app's name). */
export function useDocumentTitle(title: string) {
  useEffect(() => {
    if (typeof document !== 'undefined') document.title = title;
  }, [title]);
}
