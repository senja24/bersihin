import { createContext, useCallback, useContext, useEffect, useMemo, useReducer, useRef, useState, type ReactNode } from "react";
import { api } from "./api";
import { messageOf, sumOf, wasCancelled } from "./lib/format";
import { forgetAffected } from "./lib/hooks";
import { applyProgress, dropRun, dropTask, pick, type Slots } from "./lib/live";
import { onRunEnd } from "./lib/runEnd";
import { newRunId } from "./lib/runId";
import { runSweep } from "./lib/sweep";
import type { AiProvider, CleanResult, DiskInfo, JunkCategory, ScanProgress, ScanTask, Settings, SpaceNode, SystemInfo } from "./types";

export type View =
  | "smart-scan" | "cleanup" | "protection" | "speed" | "applications"
  | "space-map" | "large-files" | "duplicates" | "assistant" | "settings";

export interface Notice {
  id: number;
  text: string;
  tone: "ok" | "err";
}

type PrefPatch = Partial<Omit<Settings, "hasClaudeKey" | "hasOpenAiKey">>;
type Activity = "idle" | "scanning" | "cleaning";

interface Model {
  view: View;
  machine: SystemInfo | null;
  primary: DiskInfo | null;
  volumes: DiskInfo[] | null;
  prefs: Settings | null;
  junk: null | JunkCategory[];
  /** Run id of the scan behind `junk`: the scan id the clean call quotes. */
  junkRun: string | null;
  activity: Activity;
  scannedAt: number | null;
  cleanedAt: number | null;
  pulse: number;
  /** Space maps by root: "" is the home folder, otherwise a volume mount. */
  maps: Record<string, SpaceNode>;
  mapping: string[];
  /** Run id of the map being measured for each root, to follow its progress. */
  mapRuns: Record<string, string>;
  mapFailed: string[];
  notices: Notice[];
}

type Action =
  | { kind: "view"; view: View }
  | { kind: "machine"; machine: SystemInfo }
  | { kind: "disks"; primary?: DiskInfo; volumes?: DiskInfo[] }
  | { kind: "prefs"; prefs: Settings }
  | { kind: "scan-start" }
  | { kind: "scan-end"; junk?: JunkCategory[]; runId?: string }
  | { kind: "clean-start" }
  | { kind: "clean-end"; ok: boolean; ids: string[]; partial: boolean }
  | { kind: "map-start"; root: string; runId: string }
  | { kind: "map-end"; root: string; tree?: SpaceNode; stale?: boolean }
  | { kind: "maps-forget" }
  | { kind: "notice"; notice: Notice }
  | { kind: "notice-gone"; id: number };

const initial: Model = {
  view: "smart-scan", machine: null, primary: null, volumes: null, prefs: null, junk: null, junkRun: null, activity: "idle",
  scannedAt: null, cleanedAt: null, pulse: 0, maps: {}, mapping: [], mapRuns: {}, mapFailed: [], notices: [],
};

const without = (list: string[], x: string) => list.filter((y) => y !== x);
const withoutKey = (rec: Record<string, string>, k: string) => Object.fromEntries(Object.entries(rec).filter(([key]) => key !== k));

function reduce(m: Model, a: Action): Model {
  switch (a.kind) {
    case "view":
      return { ...m, view: a.view };
    case "machine":
      return { ...m, machine: a.machine };
    case "disks":
      return { ...m, primary: a.primary ?? m.primary, volumes: a.volumes ?? m.volumes };
    case "prefs":
      return { ...m, prefs: a.prefs };
    case "scan-start":
      return { ...m, activity: "scanning" };
    case "scan-end":
      return {
        ...m,
        activity: "idle",
        junk: a.junk ?? m.junk,
        junkRun: a.junk && a.runId ? a.runId : m.junkRun,
        scannedAt: a.junk ? Date.now() : m.scannedAt,
      };
    case "clean-start":
      return { ...m, activity: "cleaning" };
    case "clean-end":
      return {
        ...m,
        activity: "idle",
        ...(a.ok
          ? {
              pulse: m.pulse + 1,
              cleanedAt: Date.now(),
              // a partial clean leaves things behind, so the caller rescans instead of zeroing
              junk: a.partial ? m.junk : m.junk && m.junk.map((c) => (a.ids.includes(c.id) ? { ...c, size: 0, count: 0 } : c)),
            }
          : {}),
      };
    case "map-start":
      return { ...m, mapping: [...without(m.mapping, a.root), a.root], mapRuns: { ...m.mapRuns, [a.root]: a.runId }, mapFailed: without(m.mapFailed, a.root) };
    case "map-end": {
      const mapping = without(m.mapping, a.root);
      const mapRuns = withoutKey(m.mapRuns, a.root);
      // a map measured before the files changed is thrown away, and it is not a failure
      if (a.stale) return { ...m, mapping, mapRuns };
      return {
        ...m,
        mapping,
        mapRuns,
        maps: a.tree ? { ...m.maps, [a.root]: a.tree } : m.maps,
        mapFailed: a.tree ? m.mapFailed : [...without(m.mapFailed, a.root), a.root],
      };
    }
    case "maps-forget":
      return { ...m, maps: {}, mapFailed: [] };
    case "notice":
      return { ...m, notices: [...m.notices, a.notice] };
    case "notice-gone":
      return { ...m, notices: m.notices.filter((n) => n.id !== a.id) };
  }
}

interface Actions {
  navigate: (v: View) => void;
  rescan: () => Promise<void>;
  abort: (tasks?: ScanTask[]) => void;
  remap: (root?: string) => Promise<void>;
  sweep: (ids: string[]) => Promise<CleanResult | null>;
  reloadDisks: () => Promise<void>;
  /**
   * Call after files were trashed or an app was uninstalled: forgets every remembered scan that could list them,
   * drops the space maps (they are measured again when next shown) and reads the disks again.
   */
  afterChange: () => void;
  setPref: (patch: PrefPatch) => Promise<void>;
  setKey: (provider: AiProvider, key: string | null) => Promise<void>;
  notify: (text: string, tone?: Notice["tone"]) => void;
}

export type AppState = Model &
  Actions & {
    scanning: boolean;
    cleaning: boolean;
    /** What a single Clean press frees: the safe categories. */
    reclaimable: number;
  };

const Ctx = createContext<AppState | null>(null);
// Progress ticks arrive several times a second, so they live apart from the rest of the state.
const LiveCtx = createContext<Slots>({});

export function AppProvider({ children }: { children: ReactNode }) {
  const [model, send] = useReducer(reduce, initial);
  const runningScan = useRef<Promise<void> | null>(null);
  const runningMaps = useRef(new Map<string, Promise<void>>());
  const cleaningNow = useRef(false);
  const [live, setLive] = useState<Slots>({});
  const onProgress = useCallback((p: ScanProgress) => setLive((s) => applyProgress(s, p)), []);
  const forgetRun = useCallback((id: string) => setLive((s) => dropRun(s, id)), []);
  const forgetTask = useCallback((task: ScanTask) => setLive((s) => dropTask(s, task)), []);
  // bumped when files change, so a map that was being measured meanwhile is thrown away
  const changes = useRef(0);

  const notify = useCallback((text: string, tone: Notice["tone"] = "ok") => {
    const id = Math.random() + Date.now();
    send({ kind: "notice", notice: { id, text, tone } });
    setTimeout(() => send({ kind: "notice-gone", id }), 3800);
  }, []);

  const reloadDisks = useCallback(async () => {
    const [primary, volumes] = await Promise.allSettled([api.disk(), api.disks()]);
    send({
      kind: "disks",
      ...(primary.status === "fulfilled" ? { primary: primary.value } : {}),
      ...(volumes.status === "fulfilled" ? { volumes: volumes.value } : {}),
    });
  }, []);

  const remap = useCallback(
    (root?: string) => {
      const key = typeof root === "string" ? root : "";
      const existing = runningMaps.current.get(key);
      if (existing) return existing;
      const runId = newRunId();
      const born = changes.current;
      send({ kind: "map-start", root: key, runId });
      const job = api
        .scanSpaces(runId, key || undefined)
        .then(
          (tree) => send({ kind: "map-end", root: key, tree, stale: born !== changes.current }),
          (err) => {
            send({ kind: "map-end", root: key, stale: born !== changes.current });
            if (!wasCancelled(err)) notify(messageOf(err, "Could not measure your folders"), "err");
          },
        )
        .finally(() => {
          runningMaps.current.delete(key);
          forgetRun(runId);
        });
      runningMaps.current.set(key, job);
      return job;
    },
    [notify, forgetRun],
  );

  const rescan = useCallback((): Promise<void> => {
    if (runningScan.current) return runningScan.current;
    if (cleaningNow.current) return Promise.resolve();
    send({ kind: "scan-start" });
    const runId = newRunId();
    const job = Promise.all([api.scanJunk(runId), reloadDisks()])
      .then(
        ([junk]) => {
          send({ kind: "scan-end", junk, runId });
          void remap();
        },
        (err) => {
          send({ kind: "scan-end" });
          if (!wasCancelled(err)) notify(messageOf(err, "Scan failed"), "err");
        },
      )
      .finally(() => {
        runningScan.current = null;
        forgetRun(runId);
      });
    runningScan.current = job;
    return job;
  }, [reloadDisks, remap, notify, forgetRun]);

  const afterChange = useCallback(() => {
    changes.current += 1;
    forgetAffected();
    send({ kind: "maps-forget" });
    void reloadDisks();
  }, [reloadDisks]);

  const sweep = useCallback(
    async (ids: string[]): Promise<CleanResult | null> => {
      if (ids.length === 0 || cleaningNow.current || runningScan.current) return null;
      cleaningNow.current = true;
      send({ kind: "clean-start" });
      try {
        return await runSweep(ids, {
          scanId: model.junkRun,
          scannedAt: model.scannedAt,
          now: Date.now,
          clean: (scanId, list) => api.cleanJunk(scanId, list),
          // released before the rescan starts, which refuses to run while a clean is going
          finish: ({ ok, partial }) => {
            cleaningNow.current = false;
            send({ kind: "clean-end", ok, ids, partial });
          },
          afterChange,
          rescan,
          notify,
        });
      } finally {
        cleaningNow.current = false;
        forgetTask("clean");
      }
    },
    [model.junkRun, model.scannedAt, rescan, afterChange, notify, forgetTask],
  );

  const setPref = useCallback(async (patch: PrefPatch) => {
    send({ kind: "prefs", prefs: await api.setSettings(patch) });
  }, []);
  const setKey = useCallback(async (provider: AiProvider, key: string | null) => {
    send({ kind: "prefs", prefs: await api.setApiKey(provider, key) });
  }, []);
  const navigate = useCallback((view: View) => send({ kind: "view", view }), []);
  const abort = useCallback((tasks?: ScanTask[]) => void api.cancelScan(tasks), []);

  useEffect(() => {
    const stopProgress = api.onProgress(onProgress);
    // a finished run leaves no progress behind, so the next scan of that kind starts from nothing
    const stopRuns = onRunEnd(forgetRun);
    const unsubscribe = () => (stopProgress(), stopRuns());
    api.system().then((machine) => send({ kind: "machine", machine }), () => undefined);
    api.getSettings().then((prefs) => send({ kind: "prefs", prefs }), () => undefined);
    void rescan();
    return unsubscribe;
  }, [rescan, onProgress, forgetRun]);

  const reclaimable = useMemo(() => sumOf((model.junk ?? []).filter((c) => c.safe).map((c) => c.size)), [model.junk]);

  const value = useMemo<AppState>(
    () => ({
      ...model,
      scanning: model.activity === "scanning",
      cleaning: model.activity === "cleaning",
      reclaimable,
      navigate, rescan, abort, remap, sweep, reloadDisks, afterChange, setPref, setKey, notify,
    }),
    [model, reclaimable, navigate, rescan, abort, remap, sweep, reloadDisks, afterChange, setPref, setKey, notify],
  );
  return (
    <Ctx.Provider value={value}>
      <LiveCtx.Provider value={live}>{children}</LiveCtx.Provider>
    </Ctx.Provider>
  );
}

export function useApp(): AppState {
  const value = useContext(Ctx);
  if (!value) throw new Error("useApp needs an AppProvider above it");
  return value;
}

/** The newest progress report among the given tasks (any task when none are given), or null when nothing is reporting. */
export const useLive = (...tasks: ScanTask[]): ScanProgress | null => pick(useContext(LiveCtx), tasks);

/** The latest progress report of one run, by the run id the page gave it. */
export const useLiveRun = (id: string | undefined): ScanProgress | null => {
  const slots = useContext(LiveCtx);
  return id ? (slots[id] ?? null) : null;
};

/** Which volume a path is on, and the folder within it. Null inside the home folder. */
export function volumeOf(path: string, home: string | undefined, volumes: DiskInfo[] | null): { name: string; dir: string } | null {
  if (home && (path === home || path.startsWith(home + "/"))) return null;
  const host = (volumes ?? [])
    .filter((v) => v.mount === "/" || path.startsWith(v.mount + "/"))
    .sort((a, b) => b.mount.length - a.mount.length)[0];
  if (!host) return null;
  const folder = path.slice(0, path.lastIndexOf("/"));
  return { name: host.label, dir: (host.mount === "/" ? folder : folder.slice(host.mount.length)) || "/" };
}
