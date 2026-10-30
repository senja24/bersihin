// The only module that talks to the backend. The rest of the page imports
// `api` and `isDemo` from here and never touches the runtime itself.
import { isMyGo } from "mygo-runtime";
import { Apps, Assistant, Confirm, Files, Junk, Protection, Settings, Startup, System, events } from "./mygo";
import type * as Go from "./mygo";
import { createGuarded, rejectAsError } from "./lib/guarded";
import { runEnded } from "./lib/runEnd";
import { newRunId } from "./lib/runId";
import { createMockApi } from "./mock";
import type * as Page from "./types";
import type { BersihinApi } from "./types";

/** True when the page runs in a plain browser instead of a MyGo window. */
export const isDemo = !isMyGo();

// src/types.ts is written by hand, src/mygo.ts is generated. If a field or a name drifts apart, one of these
// stops being `true` and tsc fails, so the page can never quietly disagree with Go about a result.
type Same<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;
type MustBeTrue<T extends true> = T;
// SystemInfo.platform is "darwin" here but a plain string in Go (it always says darwin in a real build)
type NoPlatform<T> = Omit<T, "platform">;
export type ContractChecks = [
  MustBeTrue<Same<Page.CleanResult, Go.CleanResult>>,
  MustBeTrue<Same<Page.JunkCategory, Go.JunkCategory>>,
  MustBeTrue<Same<Page.ScanProgress, Go.ScanProgress>>,
  MustBeTrue<Same<Page.ScanTask, Go.ScanTask>>,
  MustBeTrue<Same<Page.UninstallResult, Go.UninstallResult>>,
  MustBeTrue<Same<Page.DiskInfo, Go.DiskInfo>>,
  MustBeTrue<Same<Page.SpaceNode, Go.SpaceNode>>,
  MustBeTrue<Same<Page.LargeFile, Go.LargeFile>>,
  MustBeTrue<Same<Page.DuplicateGroup, Go.DuplicateGroup>>,
  MustBeTrue<Same<Page.LargeScan, Go.LargeScan>>,
  MustBeTrue<Same<Page.DuplicateScan, Go.DuplicateScan>>,
  MustBeTrue<Same<Page.StartupItem, Go.StartupItem>>,
  MustBeTrue<Same<Page.InstalledApp, Go.InstalledApp>>,
  MustBeTrue<Same<Page.ProtectionStatus, Go.ProtectionStatus>>,
  MustBeTrue<Same<Page.Settings, Go.Settings>>,
  MustBeTrue<Same<Page.AiProvider, Go.AiProvider>>,
  MustBeTrue<Same<Page.KeyState, Go.KeyState>>,
  MustBeTrue<Same<Page.AiEndpoint, Go.AiEndpoint>>,
  MustBeTrue<Same<Page.AiEndpointPatch, Go.AiEndpointPatch>>,
  MustBeTrue<Same<Page.AiTestResult, Go.AiTestResult>>,
  MustBeTrue<Same<Page.SettingsPatch, Go.SettingsPatch>>,
  MustBeTrue<Same<Page.AiAdvice, Go.AiAdvice>>,
  MustBeTrue<Same<Page.IconName, Go.IconName>>,
  MustBeTrue<Same<NoPlatform<Page.SystemInfo>, NoPlatform<Go.SystemInfo>>>,
];

function createBackendApi(): BersihinApi {
  return {
    // the Go side always reports "darwin" in a real build
    system: () => rejectAsError(System.info() as Promise<Page.SystemInfo>),
    disk: () => rejectAsError(System.disk()),
    disks: () => rejectAsError(System.disks()),
    fileIcons: (paths) => rejectAsError(System.fileIcons(paths)),
    // scans, clean, trash, uninstall and startup changes: run ids and confirmation tokens (lib/guarded.ts)
    ...createGuarded({ confirm: Confirm, junk: Junk, files: Files, apps: Apps, startup: Startup, system: System }, newRunId, runEnded),
    revealPath: (path) => rejectAsError(System.revealPath(path)),
    startupItems: () => rejectAsError(Startup.list()),
    protection: () => rejectAsError(Protection.status()),
    getSettings: () => rejectAsError(Settings.get()),
    setSettings: (patch) => rejectAsError(Settings.set(patch)),
    setApiKey: (provider, key, forBaseUrl) => rejectAsError(Settings.setApiKey(provider, key, forBaseUrl)),
    askAi: (question, context) => rejectAsError(Assistant.ask(question, context)),
    testConnection: (provider, draft) => rejectAsError(Assistant.testConnection(provider, draft)),
    openExternal: (url) => rejectAsError(System.openExternal(url)),
    onProgress: (cb) => events.progress.on(cb),
    onWindowFocus: (cb) => events.windowFocus.on(cb),
  };
}

export const api: BersihinApi = isDemo ? createMockApi() : createBackendApi();
