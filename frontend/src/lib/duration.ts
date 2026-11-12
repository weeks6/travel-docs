// Trip durations: seconds in the API, hours and minutes on screen.

/** "1 h 35 min", "45 min", "2 d 3 h", "30 s". */
export function formatDuration(seconds: number): string {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s} s`;
  const minutes = Math.round(s / 60);
  const d = Math.floor(minutes / 1440);
  const h = Math.floor((minutes % 1440) / 60);
  const m = minutes % 60;
  if (d > 0) return h > 0 ? `${d} d ${h} h` : `${d} d`;
  if (h > 0) return m > 0 ? `${h} h ${m} min` : `${h} h`;
  return `${m} min`;
}

/** Splits seconds into whole hours and minutes for form fields. */
export function toHoursMinutes(seconds: number): { hours: number; minutes: number } {
  const minutes = Math.round(seconds / 60);
  return { hours: Math.floor(minutes / 60), minutes: minutes % 60 };
}

/**
 * Reads hours and minutes form fields as seconds. Returns null when both
 * are empty, and NaN when they don't make a positive duration.
 */
export function fromHoursMinutes(hours: string, minutes: string): number | null {
  const h = hours.trim();
  const m = minutes.trim();
  if (h === "" && m === "") return null;
  const hv = h === "" ? 0 : Number(h);
  const mv = m === "" ? 0 : Number(m);
  if (!Number.isInteger(hv) || !Number.isInteger(mv) || hv < 0 || mv < 0 || mv >= 60) return NaN;
  const s = (hv * 60 + mv) * 60;
  return s > 0 ? s : NaN;
}

/** Average speed in km/h, or null without a duration. */
export function speedKmh(distanceM: number, seconds: number): number | null {
  return seconds > 0 && distanceM > 0 ? distanceM / 1000 / (seconds / 3600) : null;
}
