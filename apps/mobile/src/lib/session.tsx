// The member session: the Kratos session token, kept in secure storage and
// in memory. The API client reads it through getSessionToken() on every
// request, so a sign-out takes effect immediately everywhere.
//
// Screens wait for `hydrated` before deciding to redirect: deciding on the
// empty initial state would flash the sign-in screen on every cold start.
import type { Session } from '@birge/api';
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

import { storage } from './storage';

const KEY = 'birge.session';

let current: string | null = null;
let expireHandler: (() => void) | null = null;

/** getSessionToken returns the token for the Authorization header, or null. */
export function getSessionToken(): string | null {
  return current;
}

/** expireSession is called on a 401 from the API: the token is no longer valid. */
export function expireSession(): void {
  expireHandler?.();
}

/** SessionState is what screens see. */
export interface SessionState {
  hydrated: boolean;
  signedIn: boolean;
  signIn(session: Session): Promise<void>;
  signOut(): Promise<void>;
}

const SessionContext = createContext<SessionState | null>(null);

/** SessionProviderProps configure the provider. */
export interface SessionProviderProps {
  children: ReactNode;
  /** revoke ends the session on the server after sign-out (best effort; the local token is gone first). */
  revoke?: (token: string) => Promise<void>;
  /**
   * beforeSignOut runs while the session is still valid, before the token is
   * dropped (the push device is unregistered here). Best effort: it never
   * blocks sign-out for longer than BEFORE_SIGN_OUT_MS, and a failure is ignored.
   */
  beforeSignOut?: () => Promise<void>;
}

/** BEFORE_SIGN_OUT_MS bounds beforeSignOut: offline, sign-out must still be quick. */
const BEFORE_SIGN_OUT_MS = 5_000;

async function bounded(fn: () => Promise<void>): Promise<void> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<void>((resolve) => {
    timer = setTimeout(resolve, BEFORE_SIGN_OUT_MS);
  });
  try {
    await Promise.race([fn().catch(() => undefined), timeout]);
  } finally {
    clearTimeout(timer);
  }
}

/** SessionProvider loads the stored token once and owns sign-in and sign-out. */
export function SessionProvider({ children, revoke, beforeSignOut }: SessionProviderProps) {
  const [hydrated, setHydrated] = useState(false);
  const [token, setToken] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    storage
      .get(KEY)
      .catch(() => null)
      .then((stored) => {
        if (!active) return;
        current = stored;
        setToken(stored);
        setHydrated(true);
      });
    return () => {
      active = false;
    };
  }, []);

  const clear = useCallback(async () => {
    current = null;
    setToken(null);
    await storage.remove(KEY).catch(() => undefined);
  }, []);

  useEffect(() => {
    expireHandler = () => void clear();
    return () => {
      expireHandler = null;
    };
  }, [clear]);

  const value = useMemo<SessionState>(
    () => ({
      hydrated,
      signedIn: token !== null,
      async signIn(session) {
        await storage.set(KEY, session.token);
        current = session.token;
        setToken(session.token);
      },
      async signOut() {
        if (beforeSignOut) await bounded(beforeSignOut);
        const old = current;
        await clear();
        if (old && revoke) await revoke(old);
      },
    }),
    [hydrated, token, clear, revoke, beforeSignOut],
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

/** useSession returns the session state; it must be used inside SessionProvider. */
export function useSession(): SessionState {
  const s = useContext(SessionContext);
  if (!s) throw new Error('useSession outside SessionProvider');
  return s;
}
