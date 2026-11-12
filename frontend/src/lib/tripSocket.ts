"use client";

// Live connection to a trip (GET /trips/{id}/ws). The socket authenticates
// with its first message, sends a fresh token before the old one expires,
// and reconnects with backoff. The server's close codes say whether to
// reconnect: 4001 refresh the token first, 4003/4004 stop for good.

import { useCallback, useEffect, useRef, useState } from "react";
import { getAccessToken, refreshSession } from "./api";
import { WS_URL } from "./config";
import type { TripMessage } from "./types";

export type { TripMessage };

export type SocketState = "connecting" | "live" | "reconnecting" | "closed";


const CLOSE_UNAUTHORIZED = 4001;
const CLOSE_FORBIDDEN = 4003;
const CLOSE_NOT_FOUND = 4004;

export function useTripSocket(tripId: string, enabled: boolean, onMessage: (m: TripMessage) => void) {
  const [state, setState] = useState<SocketState>("connecting");
  const [closedReason, setClosedReason] = useState("");
  const socket = useRef<WebSocket | null>(null);
  const handler = useRef(onMessage);
  useEffect(() => {
    handler.current = onMessage;
  });

  useEffect(() => {
    if (!enabled) return;
    let stopped = false;
    let attempt = 0;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;

    let sentToken = "";

    const schedule = (delay: number) => {
      retryTimer = setTimeout(connect, delay);
    };

    async function connect() {
      if (stopped) return;
      const token = await getAccessToken();
      if (stopped) return;
      if (!token) {
        setState("closed");
        setClosedReason("Log in to follow this trip.");
        return;
      }
      setState(attempt === 0 ? "connecting" : "reconnecting");
      const ws = new WebSocket(`${WS_URL}/trips/${tripId}/ws`);
      socket.current = ws;
      ws.onopen = () => {
        sentToken = token;
        ws.send(JSON.stringify({ type: "auth", token }));
      };
      ws.onmessage = (e) => {
        let m: TripMessage;
        try {
          m = JSON.parse(String(e.data)) as TripMessage;
        } catch {
          return;
        }
        if (m.type === "ready") {
          attempt = 0;
          setState("live");
        }
        handler.current(m);
      };
      ws.onclose = async (e) => {
        if (socket.current === ws) socket.current = null;
        if (stopped) return;
        if (e.code === CLOSE_FORBIDDEN || e.code === CLOSE_NOT_FOUND) {
          setState("closed");
          setClosedReason(e.reason || "You can no longer follow this trip.");
          return;
        }
        setState("reconnecting");
        if (e.code === CLOSE_UNAUTHORIZED) {
          const ok = await refreshSession();
          if (!ok && !(await getAccessToken())) {
            setState("closed");
            setClosedReason("Your session ended. Log in again.");
            return;
          }
        }
        // 1 s, 2 s, 4 s … 15 s, with jitter so clients don't reconnect at once.
        const delay = Math.min(15_000, 1000 * 2 ** attempt) * (0.7 + Math.random() * 0.6);
        attempt++;
        schedule(delay);
      };
    }

    // Keep the open socket authenticated: send the refreshed token, which
    // getAccessToken() renews a minute before the old one expires.
    const tokenTimer = setInterval(async () => {
      const ws = socket.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) return;
      const token = await getAccessToken();
      if (token && token !== sentToken && ws.readyState === WebSocket.OPEN) {
        sentToken = token;
        ws.send(JSON.stringify({ type: "auth", token }));
      }
    }, 30_000);

    connect();
    return () => {
      stopped = true;
      clearTimeout(retryTimer);
      clearInterval(tokenTimer);
      socket.current?.close(1000, "leaving");
      socket.current = null;
    };
  }, [tripId, enabled]);

  // Stable, so effects can depend on it.
  const send = useCallback((m: object): boolean => {
    const ws = socket.current;
    if (!ws || ws.readyState !== WebSocket.OPEN) return false;
    ws.send(JSON.stringify(m));
    return true;
  }, []);

  return { state, closedReason, send };
}
