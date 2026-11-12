"use client";

// The user's live event stream (GET /events): import job progress.
// Server-Sent Events need the Authorization header, so the
// stream is read with fetch() instead of EventSource.

import { createContext, useContext, useEffect, useRef, useState } from "react";
import { getAccessToken, refreshSession } from "./api";
import { API_URL } from "./config";
import { useSession } from "./session";
import type { UserEvent } from "./types";

type Listener = (ev: UserEvent) => void;

interface EventsState {
  connected: boolean;
  subscribe: (fn: Listener) => () => void;
}

const EventsContext = createContext<EventsState>({ connected: false, subscribe: () => () => {} });

export function EventsProvider({ children }: { children: React.ReactNode }) {
  const { user } = useSession();
  const [connected, setConnected] = useState(false);
  const listeners = useRef(new Set<Listener>());
  const [subscribe] = useState(() => (fn: Listener) => {
    listeners.current.add(fn);
    return () => {
      listeners.current.delete(fn);
    };
  });

  const userID = user?.id;
  useEffect(() => {
    if (!userID) return;
    const abort = new AbortController();
    const dispatch = (ev: UserEvent) => {
      for (const fn of listeners.current) fn(ev);
    };
    run(abort.signal, dispatch, setConnected);
    return () => {
      abort.abort();
      setConnected(false);
    };
  }, [userID]);

  return <EventsContext.Provider value={{ connected, subscribe }}>{children}</EventsContext.Provider>;
}

const sleep = (ms: number, signal: AbortSignal) =>
  new Promise<void>((resolve) => {
    const t = setTimeout(resolve, ms);
    signal.addEventListener("abort", () => {
      clearTimeout(t);
      resolve();
    });
  });

// run keeps the stream open, reconnecting with backoff until aborted.
async function run(signal: AbortSignal, dispatch: (ev: UserEvent) => void, setConnected: (v: boolean) => void) {
  let backoff = 1000;
  while (!signal.aborted) {
    let reason = "error";
    try {
      reason = await stream(signal, dispatch, () => {
        setConnected(true);
        backoff = 1000;
      });
    } catch {
      reason = "error";
    }
    setConnected(false);
    if (signal.aborted) return;
    if (reason === "reauth") {
      await refreshSession();
      continue;
    }
    // resync / shutdown: reconnect soon (to another instance); errors: back off.
    await sleep(reason === "error" ? backoff : 500 + Math.random() * 1000, signal);
    if (reason === "error") backoff = Math.min(backoff * 2, 30_000);
  }
}

// stream reads one connection and returns why it ended.
async function stream(signal: AbortSignal, dispatch: (ev: UserEvent) => void, onReady: () => void): Promise<string> {
  const token = await getAccessToken();
  if (!token) return "error";
  const res = await fetch(API_URL + "/events", {
    headers: { Authorization: "Bearer " + token, Accept: "text/event-stream" },
    cache: "no-store",
    signal,
  });
  if (res.status === 401) return "reauth";
  if (!res.ok || !res.body) return "error";

  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buffer = "";
  let event = "";
  let data = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return "error";
    buffer += value;
    let nl: number;
    while ((nl = buffer.indexOf("\n")) >= 0) {
      const line = buffer.slice(0, nl).replace(/\r$/, "");
      buffer = buffer.slice(nl + 1);
      if (line === "") {
        // End of an event.
        if (event === "ready") {
          onReady();
          dispatch({ type: "connected" });
        } else if (event === "reauth" || event === "resync" || event === "shutdown") {
          reader.cancel().catch(() => undefined);
          return event;
        } else if (data) {
          try {
            dispatch(JSON.parse(data) as UserEvent);
          } catch {
            // ignore malformed events
          }
        }
        event = "";
        data = "";
      } else if (line.startsWith("event:")) {
        event = line.slice(6).trim();
      } else if (line.startsWith("data:")) {
        data += line.slice(5).trim();
      }
    }
  }
}

/** Calls handler for every user event while the component is mounted. */
export function useUserEvents(handler: (ev: UserEvent) => void) {
  const { subscribe } = useContext(EventsContext);
  const ref = useRef(handler);
  useEffect(() => {
    ref.current = handler;
  });
  useEffect(() => subscribe((ev) => ref.current(ev)), [subscribe]);
}

export function useEventsConnected(): boolean {
  return useContext(EventsContext).connected;
}
