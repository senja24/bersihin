/** How long a remembered scan may be used. Go forgets a scan after 10 minutes, so the page lets go a minute earlier. */
export const SCAN_TTL_MS = 9 * 60_000;

/** True when a scan finished at `at` is too old to act on. */
export const isExpired = (at: number | null, now: number): boolean => at !== null && now - at >= SCAN_TTL_MS;

export interface ScanEntry<T> {
  data: T;
  /** The run id of the scan that produced the data; Go knows it as the scan id. */
  runId: string;
  /** When the scan finished. */
  at: number;
}

/** Answers of slow scans, with their run ids, kept until they are forgotten or too old. */
export function createScanMemory(now: () => number = Date.now) {
  const entries = new Map<string, ScanEntry<unknown>>();
  let epoch = 0;
  const live = (key: string) => {
    const e = entries.get(key);
    if (e && isExpired(e.at, now())) {
      entries.delete(key);
      return undefined;
    }
    return e;
  };
  return {
    get: <T>(key: string) => live(key) as ScanEntry<T> | undefined,
    has: (key: string) => live(key) !== undefined,
    set: <T>(key: string, data: T, runId: string) => void entries.set(key, { data, runId, at: now() }),
    /** Replaces the data of a remembered scan (some items were removed) without making it look newer. */
    update: <T>(key: string, data: T) => {
      const e = live(key);
      if (e) entries.set(key, { ...e, data });
    },
    delete: (key: string) => void entries.delete(key),
    /** Drops answers whose key starts with one of the prefixes. Scans that began earlier must not store their result. */
    forget: (...prefixes: string[]) => {
      epoch += 1;
      for (const k of [...entries.keys()]) if (prefixes.some((p) => k.startsWith(p))) entries.delete(k);
    },
    epoch: () => epoch,
  };
}
