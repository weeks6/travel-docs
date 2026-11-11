export const km = (m: number) => (m / 1000).toFixed(m < 10000 ? 2 : 1) + " km";
export const meters = (m: number | null | undefined) => (m == null ? "—" : Math.round(m) + " m");
export const date = (s: string) => new Date(s).toLocaleDateString();
export const dateTime = (s: string) => new Date(s).toLocaleString();
export const time = (s: string) => new Date(s).toLocaleTimeString();

export function bytes(n: number): string {
  if (n < 1024) return n + " B";
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
  return (n / 1024 / 1024).toFixed(1) + " MB";
}

// "just now", "5 min ago", "3 h ago", or a date.
export function ago(s: string, now = Date.now()): string {
  const sec = Math.max(0, Math.round((now - new Date(s).getTime()) / 1000));
  if (sec < 45) return "just now";
  if (sec < 3600) return Math.round(sec / 60) + " min ago";
  if (sec < 86400) return Math.round(sec / 3600) + " h ago";
  return date(s);
}

export function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? "" : "s"}`;
}
