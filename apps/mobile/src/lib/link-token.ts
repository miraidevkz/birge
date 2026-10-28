// The links an external trusted contact gets by SMS carry a token in the URL
// fragment: `<public URL>/plan#<token>` (contract: viewExternalPlan) and
// `<public URL>/sos-link#<token>` (contract: viewExternalSos). The fragment
// never reaches a server or its access log; the page reads it once, removes
// it from the address bar and the history entry, and keeps it only in memory
// for the request. It is never stored, logged or put into a query key.

/** LINK_TOKEN is the shape of a link token: 32 random bytes, base64url without padding. */
export const LINK_TOKEN = /^[A-Za-z0-9_-]{43}$/;

/** BrowserWindow is the part of `window` takeLinkToken needs (injectable for tests). */
export interface BrowserWindow {
  location: Pick<Location, 'hash' | 'pathname' | 'search'>;
  history: Pick<History, 'state' | 'replaceState'>;
}

/**
 * takeLinkToken reads the token from the URL fragment and clears the fragment
 * at once, valid or not, so it does not stay in the address bar, the history
 * or a later Referer. It returns null when there is no fragment, it is not a
 * well-formed token, or there is no browser window (native).
 */
export function takeLinkToken(win: BrowserWindow | undefined = browserWindow()): string | null {
  if (!win?.location || !win.history) return null;
  const raw = win.location.hash.replace(/^#/, '');
  if (win.location.hash !== '') {
    // Keep the router's history state; only the URL loses its fragment.
    win.history.replaceState(win.history.state, '', win.location.pathname + win.location.search);
  }
  return LINK_TOKEN.test(raw) ? raw : null;
}

function browserWindow(): BrowserWindow | undefined {
  const g = globalThis as Partial<BrowserWindow>;
  return g.location && g.history ? (g as BrowserWindow) : undefined;
}
