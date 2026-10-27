// Staff session around the in-memory OIDC access token. Screens wait for
// `hydrated` (always true after first paint: there is nothing to restore from
// storage) before redirecting. A reload always lands on sign-in.
import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

import {
  clearAccessToken,
  getAccessToken,
  getStaffProfile,
  registerAuthHandlers,
  setAccessToken,
  signInWithOidc,
  signOutLocally,
  type StaffProfile,
} from './auth';

/** SessionState is what screens see. */
export interface SessionState {
  hydrated: boolean;
  signedIn: boolean;
  /** mfaRequired is set when the API answered 403 mfa_required. */
  mfaRequired: boolean;
  profile: StaffProfile | null;
  signIn(): Promise<void>;
  signOut(): Promise<void>;
  clearMfa(): void;
}

const SessionContext = createContext<SessionState | null>(null);

/** SessionProvider owns sign-in / sign-out and reacts to 401 / MFA. */
export function SessionProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => getAccessToken());
  const [profile, setProfile] = useState<StaffProfile | null>(() => getStaffProfile());
  const [mfaRequired, setMfaRequired] = useState(false);

  const clear = useCallback(() => {
    signOutLocally();
    clearAccessToken();
    setToken(null);
    setProfile(null);
  }, []);

  useEffect(() => {
    registerAuthHandlers({
      onExpire: () => {
        clear();
        setMfaRequired(false);
      },
      onMfaRequired: () => {
        clear();
        setMfaRequired(true);
      },
    });
    return () => registerAuthHandlers(null);
  }, [clear]);

  const value = useMemo<SessionState>(
    () => ({
      // Nothing is restored from storage; first paint is already decided.
      hydrated: true,
      signedIn: token !== null,
      mfaRequired,
      profile,
      async signIn() {
        setMfaRequired(false);
        const snap = await signInWithOidc();
        setAccessToken(snap.accessToken, snap.profile);
        setToken(snap.accessToken);
        setProfile(snap.profile);
      },
      async signOut() {
        clear();
        setMfaRequired(false);
      },
      clearMfa() {
        setMfaRequired(false);
      },
    }),
    [token, profile, mfaRequired, clear],
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

/** useSession returns the session state; it must be used inside SessionProvider. */
export function useSession(): SessionState {
  const s = useContext(SessionContext);
  if (!s) throw new Error('useSession outside SessionProvider');
  return s;
}
