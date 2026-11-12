"use client";

import { useSyncExternalStore } from "react";
import type { Theme } from "./theme-script";

export type { Theme };

declare global {
  interface Window {
    __setTheme?: (t: Theme) => void;
  }
}

function subscribe(onChange: () => void) {
  window.addEventListener("themechange", onChange);
  return () => window.removeEventListener("themechange", onChange);
}

// The head script records the current choice on <html data-theme>.
const read = (): Theme => (document.documentElement.dataset.theme as Theme) || "system";

/** The chosen theme and a setter; "system" until hydrated. */
export function useTheme(): [Theme, (t: Theme) => void] {
  const theme = useSyncExternalStore(subscribe, read, () => "system" as Theme);
  return [theme, (t) => window.__setTheme?.(t)];
}
