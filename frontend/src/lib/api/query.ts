// Small helpers for building API requests.

export type QueryValue = string | number | boolean | null | undefined;

/** Builds "?a=1&b=2" from params, skipping empty values ("" if none are left). */
export function query(params: Record<string, QueryValue>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") q.set(k, String(v));
  }
  const s = q.toString();
  return s ? "?" + s : "";
}

/** The If-Match header for optimistic concurrency on versioned resources. */
export function ifMatch(version: number): Record<string, string> {
  return { "If-Match": `"${version}"` };
}

/** Encodes a path segment. */
export const seg = (s: string | number) => encodeURIComponent(String(s));
