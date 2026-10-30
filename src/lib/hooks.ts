import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";
import { api } from "../api";
import { wasCancelled } from "./format";
import { newRunId } from "./runId";
import { createScanMemory, isExpired } from "./scanMemory";

/** State object that is updated by merging a partial change. */
export function usePatch<T extends object>(initial: T): [T, (change: Partial<T>) => void] {
  return useReducer((cur: T, change: Partial<T>) => ({ ...cur, ...change }), initial);
}

/** A set of chosen keys with the usual operations. */
export function useChoice(initial: Iterable<string> = []) {
  const [chosen, setChosen] = useState<ReadonlySet<string>>(() => new Set(initial));
  // The operations never change, so rows that receive them can be memoised.
  const ops = useMemo(
    () => ({
      flip: (k: string) =>
        setChosen((cur) => {
          const next = new Set(cur);
          next.has(k) ? next.delete(k) : next.add(k);
          return next;
        }),
      addAll: (ks: Iterable<string>) => setChosen((cur) => new Set([...cur, ...ks])),
      removeAll: (ks: Iterable<string>) =>
        setChosen((cur) => {
          const next = new Set(cur);
          for (const k of ks) next.delete(k);
          return next;
        }),
      reset: (ks: Iterable<string> = []) => setChosen(new Set(ks)),
    }),
    [],
  );
  return { chosen, has: (k: string) => chosen.has(k), ...ops };
}

// Answers of slow scans live here for the session, each with the run id that produced it and a time limit.
const memory = createScanMemory();
const flights = new Map<string, Promise<{ data: unknown; runId: string }>>();

export interface CachedScan<T> {
  data: T | null;
  /** The run id of the scan behind `data`: the scan id that trash and uninstall calls must quote. */
  runId: string | null;
  busy: boolean;
  broken: boolean;
  /** The last run was cancelled before it produced data. */
  cancelled: boolean;
  /** True when the scan behind `data` is too old to act on; check it right before a destructive action. */
  expired: () => boolean;
  rerun: () => Promise<void>;
  /** Swaps in a corrected copy of the data (some items were removed); the run id stays. */
  replace: (next: T) => void;
}

/** Drops remembered scan answers whose key starts with one of the prefixes, e.g. after files were trashed. */
export function forgetScans(...prefixes: string[]) {
  memory.forget(...prefixes);
  for (const k of [...flights.keys()]) if (prefixes.some((p) => k.startsWith(p))) flights.delete(k);
}

/** Forgets every scan that could list files an action just moved or removed (H3). */
export const forgetAffected = () => forgetScans("large:", "duplicates", "apps");

/**
 * Runs `load` once per key and remembers the answer; `rerun` forces a fresh scan. `load` gets the run id to give to
 * the backend, a new one for every real scan.
 */
export function useCachedScan<T>(key: string | null, load: (runId: string) => Promise<T>, onFail: (e: unknown) => void): CachedScan<T> {
  const recall = () => (key !== null ? memory.get<T>(key) : undefined);
  const [view, patch] = usePatch<{ data: T | null; runId: string | null; at: number | null; busy: boolean; broken: boolean; cancelled: boolean }>({
    data: recall()?.data ?? null, runId: recall()?.runId ?? null, at: recall()?.at ?? null, busy: false, broken: false, cancelled: false,
  });
  const fns = useRef({ load, onFail });
  fns.current = { load, onFail };
  const current = useRef({ key, run: 0 });
  current.current.key = key;

  const fetchFresh = useCallback(
    async (force: boolean) => {
      if (key === null) return;
      const known = memory.get<T>(key);
      if (!force && known) {
        patch({ data: known.data, runId: known.runId, at: known.at });
        return;
      }
      memory.delete(key);
      const run = ++current.current.run;
      const born = memory.epoch();
      const stale = () => current.current.key !== key || current.current.run !== run;
      patch({ busy: true, broken: false, cancelled: false });
      try {
        let flight = flights.get(key) as Promise<{ data: T; runId: string }> | undefined;
        if (!flight) {
          const runId = newRunId();
          flight = fns.current.load(runId).then((data) => ({ data, runId })).finally(() => flights.delete(key));
          flights.set(key, flight);
        }
        const { data, runId } = await flight;
        const at = Date.now();
        if (born === memory.epoch()) memory.set(key, data, runId);
        if (!stale()) patch({ data, runId, at });
      } catch (e) {
        if (!stale() && wasCancelled(e)) patch({ cancelled: true });
        else if (!stale()) {
          patch({ broken: true });
          fns.current.onFail(e);
        }
      } finally {
        if (!stale()) patch({ busy: false });
      }
    },
    [key, patch],
  );

  useEffect(() => {
    patch({ data: recall()?.data ?? null, runId: recall()?.runId ?? null, at: recall()?.at ?? null, busy: false, cancelled: false });
    void fetchFresh(false);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberate
  }, [key, fetchFresh]);

  return {
    ...view,
    expired: () => view.data !== null && isExpired(view.at, Date.now()),
    rerun: () => fetchFresh(true),
    replace: (next) => {
      if (key !== null) memory.update(key, next);
      patch({ data: next });
    },
  };
}

/** System icons (data URLs) for the paths, filled in as they arrive. */
export function useIcons(paths: (string | undefined | null)[]): Record<string, string> {
  const [found, setFound] = useState<Record<string, string>>({});
  const requested = useRef(new Set<string>());
  const wanted = paths.filter((p): p is string => !!p).join("\n");
  useEffect(() => {
    const fresh = [...new Set(wanted.split("\n"))].filter((p) => p && !requested.current.has(p));
    if (!fresh.length) return;
    fresh.forEach((p) => requested.current.add(p));
    api.fileIcons(fresh).then((got) => setFound((cur) => ({ ...cur, ...got })), () => undefined);
  }, [wanted]);
  return found;
}

/** Follows a number to its target with an ease-out tween. */
export function useTween(target: number, ms = 900): number {
  const calm = typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
  const [value, setValue] = useState(calm ? target : 0);
  const from = useRef(value);
  useEffect(() => {
    if (calm) {
      from.current = target;
      setValue(target);
      return;
    }
    const start = from.current;
    const began = performance.now();
    let frameId = 0;
    const draw = (t: number) => {
      const p = Math.min(1, (t - began) / ms);
      from.current = start + (target - start) * (1 - Math.pow(1 - p, 3));
      setValue(from.current);
      if (p < 1) frameId = requestAnimationFrame(draw);
    };
    frameId = requestAnimationFrame(draw);
    return () => cancelAnimationFrame(frameId);
  }, [target, ms, calm]);
  return value;
}
