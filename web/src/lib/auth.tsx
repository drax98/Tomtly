import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { session } from "@/api";
import { authApi, type AuthStatus, type Permission, type User } from "@/authApi";

export interface AuthState {
  /** null until the first answer, so the screen can show a skeleton instead of flashing the login form. */
  status: AuthStatus | null;
  /** The session ended while a page was open; the page stays mounted underneath the sign-in screen. */
  expired: boolean;
  unreachable: boolean;
  /** True right after the first account is created, to show the welcome. */
  welcome: boolean;
  setWelcome: (on: boolean) => void;
  can: (permission: Permission) => boolean;
  refresh: () => Promise<void>;
  /** Login and setup answer with the new status, so it can be used as is. */
  adopt: (status: AuthStatus) => void;
  signedOut: () => void;
}

export const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus | null>(null);
  const [expired, setExpired] = useState(false);
  const [unreachable, setUnreachable] = useState(false);
  const [welcome, setWelcome] = useState(false);

  const refresh = useCallback(async () => {
    try {
      setStatus(await authApi.status());
      setUnreachable(false);
      session.expired = false;
      setExpired(false);
    } catch {
      setUnreachable(true);
    }
  }, []);

  useEffect(() => {
    session.onExpired = () => {
      session.expired = true;
      setExpired(true);
    };
    void refresh();
    return () => {
      session.onExpired = () => {};
    };
  }, [refresh]);

  const adopt = useCallback((next: AuthStatus) => {
    setStatus(next);
    setUnreachable(false);
    session.expired = false;
    setExpired(false);
  }, []);

  const signedOut = useCallback(() => {
    session.expired = false;
    setExpired(false);
    setStatus((s) => (s ? { ...s, user: null, permissions: [] } : s));
  }, []);

  const value = useMemo<AuthState>(() => ({
    status,
    expired,
    unreachable,
    welcome,
    setWelcome,
    can: (permission) => status?.permissions.includes(permission) ?? false,
    refresh,
    adopt,
    signedOut,
  }), [status, expired, unreachable, welcome, refresh, adopt, signedOut]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth needs an AuthProvider");
  return ctx;
}

/** The signed-in user. Only call below the gate that shows the sign-in screen. */
export function useUser(): User {
  const user = useAuth().status?.user;
  if (!user) throw new Error("No signed-in user");
  return user;
}
