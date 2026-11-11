// Base URL of the Travel Docs API. NEXT_PUBLIC_ variables are inlined at
// build time, so set it before `next build`.
export const API_URL = (process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080").replace(/\/+$/, "");

// WebSocket base URL (ws:// or wss://) of the same API.
export const WS_URL = API_URL.replace(/^http/, "ws");
