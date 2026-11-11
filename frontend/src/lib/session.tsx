"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { currentSessionUser, initSession, onSessionChange } from "./api";
import type { User } from "./types";

interface SessionState {
  user: User | null;
  // false until the stored session has been restored (or found missing).
  ready: boolean;
}

const SessionContext = createContext<SessionState>({ user: null, ready: false });

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<SessionState>({ user: null, ready: false });
  useEffect(() => {
    let active = true;
    const off = onSessionChange((user) => setState({ user, ready: true }));
    initSession().then(() => {
      if (active) setState({ user: currentSessionUser(), ready: true });
    });
    return () => {
      active = false;
      off();
    };
  }, []);
  return <SessionContext.Provider value={state}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionState {
  return useContext(SessionContext);
}
