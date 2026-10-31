import { isWinPath, leafOf, parentOf, within } from "./platform";

const SCALE = ["B", "KB", "MB", "GB", "TB"] as const;

/** Splits a byte count into a display number and its unit. */
export function splitBytes(n: number): { value: string; unit: string } {
  if (!isFinite(n) || n <= 0) return { value: "0", unit: "GB" };
  let step = 0;
  let v = n;
  for (; v >= 1000 && step < SCALE.length - 1; step++) v /= 1024;
  const digits = step === 0 || v >= 100 ? 0 : 1;
  return { value: v.toFixed(digits), unit: SCALE[step] ?? "B" };
}

export const formatBytes = (n: number): string => {
  const p = splitBytes(n);
  return p.value + " " + p.unit;
};

const SPANS: [string, number][] = [
  ["year", 365 * 86400],
  ["month", 30 * 86400],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
];

/** "3 days ago", or "never" for a missing time. */
export function timeAgo(when?: number | null, now = Date.now()): string {
  if (!when) return "never";
  const secs = (now - when) / 1000;
  for (const [name, len] of SPANS) {
    if (secs >= len) {
      const n = Math.floor(secs / len);
      return `${n} ${name}${n === 1 ? "" : "s"} ago`;
    }
  }
  return "just now";
}

export const formatDate = (when?: number | null): string =>
  when ? new Date(when).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) : "-";

/** Writes the home folder as ~ (~\Downloads on Windows, where names compare without case). */
export function tildePath(path: string, home?: string): string {
  if (!home) return path;
  if (isWinPath(home)) return within(home, path) ? "~" + path.slice(home.replace(/[\\/]+$/, "").length) : path;
  return path === home || path.startsWith(home + "/") ? "~" + path.slice(home.length) : path;
}

/** True for junk that is deleted for good (Go sets the flag on the category: every cache, log and the Trash). */
export const isIrreversible = (c: { irreversible: boolean }) => c.irreversible;

/** "1 file", "2 files". */
export const countOf = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** The quiet line for items the safety rules kept out of a result, or null when nothing was kept out. */
export const hiddenNote = (n: number, one: string, many: string) => (n > 0 ? `${countOf(n, one, many)} hidden by safety rules` : null);

export const leafName = leafOf;
export const dirOf = parentOf;
export const sumOf = (nums: number[]) => nums.reduce((acc, n) => acc + n, 0);
export const wasCancelled = (err: unknown) => err instanceof Error && /cancel/i.test(err.message);
export const messageOf = (err: unknown, fallback: string) => (err instanceof Error && err.message ? err.message : fallback);
