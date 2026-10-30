// The calls that need a confirmation token or a run id, written against a small interface of the generated Go
// client so they can be tested. src/api.ts passes the real client in; it is the only module that imports it.
import type { BersihinApi, CleanResult, DuplicateScan, InstalledApp, JunkCategory, LargeScan, ScanTask, SpaceNode, StartupItem, UninstallResult } from "../types";

export interface GoBackend {
  confirm: {
    clean(scanID: string, ids: string[]): Promise<string>;
    trash(scanID: string, paths: string[]): Promise<string>;
    uninstall(scanID: string, id: string): Promise<string>;
    startup(id: string, enabled: boolean): Promise<string>;
  };
  junk: {
    scan(runID: string): Promise<JunkCategory[]>;
    clean(runID: string, scanID: string, ids: string[], token: string): Promise<CleanResult>;
  };
  files: {
    scanSpaces(runID: string, root: string): Promise<SpaceNode>;
    scanLargeFiles(runID: string, minBytes: number): Promise<LargeScan>;
    scanDuplicates(runID: string): Promise<DuplicateScan>;
    trashPaths(runID: string, scanID: string, paths: string[], token: string): Promise<CleanResult>;
  };
  apps: {
    list(runID: string): Promise<InstalledApp[]>;
    uninstall(scanID: string, id: string, token: string): Promise<UninstallResult>;
  };
  startup: { set(id: string, enabled: boolean, token: string): Promise<StartupItem[]> };
  system: { cancelScan(runID: string): Promise<boolean> };
}

/** Go errors reject with an Error whose message is the Go message; keep it that way. */
export async function rejectAsError<T>(p: Promise<T>): Promise<T> {
  try {
    return await p;
  } catch (err) {
    if (err instanceof Error) throw err;
    throw new Error(typeof err === "string" ? err : String(err));
  }
}

type Guarded = Pick<
  BersihinApi,
  | "scanJunk" | "cleanJunk" | "scanSpaces" | "scanLargeFiles" | "scanDuplicates" | "trashPaths"
  | "installedApps" | "uninstallApp" | "setStartupItem" | "cancelScan"
>;

const STOPPED: CleanResult = { freed: 0, removed: 0, failed: 0, denied: 0, skipped: [], notes: [], cancelled: true };

export function createGuarded(go: GoBackend, mintRunId: () => string, ended: (runId: string) => void = () => undefined): Guarded {
  // A MyGo call cannot be aborted from JS, so remember which runs are going and ask Go to cancel them by id.
  // `started` is false while a confirmation token is being fetched: Go does not know the run yet, so a Stop in that
  // window is remembered in `stopped` and the call is never sent.
  const running = new Map<string, { task: ScanTask; started: boolean; stopped: boolean }>();
  const track = <T>(task: ScanTask, runId: string, call: () => Promise<T>): Promise<T> => {
    running.set(runId, { task, started: true, stopped: false });
    return rejectAsError(call()).finally(() => {
      running.delete(runId);
      ended(runId);
    });
  };
  // The token is asked for right before the call it unlocks, so it is fresh and covers exactly these arguments.
  const confirmed = async <T>(mint: () => Promise<string>, call: (token: string) => Promise<T>): Promise<T> =>
    call(await rejectAsError(mint()));
  // A clean or trash run that can be stopped from the first moment, including while the token is minted.
  const stoppable = async (task: ScanTask, mint: () => Promise<string>, call: (runId: string, token: string) => Promise<CleanResult>): Promise<CleanResult> => {
    const runId = mintRunId();
    const run = { task, started: false, stopped: false };
    running.set(runId, run);
    try {
      const token = await rejectAsError(mint());
      if (run.stopped) return { ...STOPPED };
      run.started = true;
      return await rejectAsError(call(runId, token));
    } finally {
      running.delete(runId);
      ended(runId);
    }
  };

  return {
    scanJunk: (runId) => track("junk", runId, () => go.junk.scan(runId)),
    cleanJunk: (scanId, ids) => stoppable("clean", () => go.confirm.clean(scanId, ids), (runId, token) => go.junk.clean(runId, scanId, ids, token)),
    scanSpaces: (runId, root) => track("spaces", runId, () => go.files.scanSpaces(runId, root ?? "")),
    scanLargeFiles: (runId, minBytes) => track("large", runId, () => go.files.scanLargeFiles(runId, minBytes)),
    scanDuplicates: (runId) => track("dupes", runId, () => go.files.scanDuplicates(runId)),
    trashPaths: (scanId, paths) => stoppable("trash", () => go.confirm.trash(scanId, paths), (runId, token) => go.files.trashPaths(runId, scanId, paths, token)),
    installedApps: (runId) => track("apps", runId, () => go.apps.list(runId)),
    uninstallApp: (scanId, id) => confirmed(() => go.confirm.uninstall(scanId, id), (token) => rejectAsError(go.apps.uninstall(scanId, id, token))),
    setStartupItem: (id, enabled) => confirmed(() => go.confirm.startup(id, enabled), (token) => rejectAsError(go.startup.set(id, enabled, token))),
    cancelScan: async (tasks) => {
      const targets = [...running].filter(([, run]) => !tasks || tasks.includes(run.task));
      await Promise.all(
        targets.map(([id, run]) => {
          if (!run.started) {
            run.stopped = true;
            return undefined;
          }
          return go.system.cancelScan(id).catch(() => false);
        }),
      );
    },
  };
}
