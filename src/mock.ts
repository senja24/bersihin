// Demo backend for a plain browser: an invented Mac and its invented owner.
// Calls wait a little and long scans report progress, so every loading and
// result state can be seen without the Go side.
import { hostChanged, isCompatible, isReady } from "./lib/aiEndpoint";
import { checkModel, parseEndpoint } from "./lib/endpointParse";
import { runEnded } from "./lib/runEnd";
import { RUN_ID_PATTERN } from "./lib/runId";
import { SCAN_TTL_MS } from "./lib/scanMemory";
import type {
  AiAdvice, AiContext, AiEndpoint, AiEndpointPatch, AiProvider, AiTestResult, BersihinApi, CleanResult, DiskInfo, DuplicateGroup, IconName, InstalledApp, JunkCategory, LargeFile,
  ProtectionStatus, ScanProgress, Settings, SpaceNode, StartupItem, SystemInfo,
} from "./types";

const MIB = 1024 ** 2;
const GIB = 1024 ** 3;
const HOUR_MS = 3_600_000;
const DAY_MS = 24 * HOUR_MS;
const HOME = "/Users/maya";
const daysAgo = (d: number) => Date.now() - d * DAY_MS;
// Same rules and messages as Go's endpoint.Parse, so demo mode refuses what the real app refuses.
const normalizeUrl = (raw: string): string => {
  const r = parseEndpoint(raw.trim());
  if (!r.ok) throw new Error(r.error);
  return r.value.base;
};
const pause = (ms: number) => new Promise<void>((done) => setTimeout(done, ms));

const MACHINE: SystemInfo = { platform: "darwin", arch: "arm64", osVersion: "macOS 15.3", appVersion: "0.1.0", homeDir: HOME };

const VOLUMES: DiskInfo[] = [
  { mount: "/", label: "Macintosh HD", total: 460 * GIB, used: 301 * GIB, free: 159 * GIB, system: true },
  { mount: "/Volumes/Studio", label: "Studio Drive", total: 2048 * GIB, used: 1180 * GIB, free: 868 * GIB, system: false },
];

const junkRow = (id: string, name: string, icon: IconName, bytes: number, count: number, safe: boolean, description: string, ...locations: string[]): JunkCategory => ({
  id, name, icon, size: Math.round(bytes), count, safe, description, locations,
  // Go: everything is deleted for good except the old downloads, which move to the Trash
  irreversible: id !== "old-downloads",
});
const JUNK: JunkCategory[] = [
  junkRow("mac-caches", "App caches", "layers", 5.6 * GIB, 840, true, "Data apps store to load faster; they quietly rebuild it later.", "~/Library/Caches"),
  junkRow("browser-cache", "Browser caches", "globe", 2.3 * GIB, 1630, true, "Saved page assets from Safari and Chrome. Websites simply fetch them again.", "~/Library/Caches/com.apple.Safari", "~/Library/Caches/Google/Chrome/Default/Cache"),
  junkRow("xcode", "Xcode build files", "code", 3.8 * GIB, 52, true, "Intermediate build output that Xcode regenerates on the next build.", "~/Library/Developer/Xcode/DerivedData"),
  junkRow("mac-logs", "Logs and crash reports", "warning", 276 * MIB, 905, true, "Diagnostic logs and reports left behind by apps that quit unexpectedly.", "~/Library/Logs", "~/Library/Logs/DiagnosticReports"),
  junkRow("old-downloads", "Old downloads", "down", 12.4 * GIB, 87, false, "Anything in Downloads untouched for three months. It moves to the Trash.", "~/Downloads"),
  junkRow("dev-cache", "Developer caches", "code", 2.9 * GIB, 140, false, "Package manager downloads (npm, pip, Gradle). Fetched again at the next install.", "~/.npm/_cacache", "~/Library/Caches/pip", "~/.gradle/caches"),
  junkRow("trash", "Trash", "trash", 0.7 * GIB, 19, false, "Items you already threw away. Emptying the Trash is final.", "~/.Trash"),
];

// ---- folder tree ----
type Leaf = [name: string, gib: number, icon: IconName, reclaim?: number];
const folderNode = (parent: string, [name, gib, icon, reclaim = 0]: Leaf, kids?: Leaf[]): SpaceNode => {
  const path = `${parent}/${name}`;
  return {
    name, path, size: Math.round(gib * GIB), kind: "folder", icon, reclaimable: Math.round(reclaim * GIB),
    ...(kids ? { children: kids.map((k) => folderNode(path, k)) } : {}),
  };
};
const HOME_TREE: [Leaf, Leaf[]?][] = [
  [["Developer", 88, "code"], [["Xcode", 38, "folder", 3.8], ["Repos", 29, "folder"], ["Simulators", 14, "folder"], ["Containers", 7, "folder"]]],
  [["Library", 63.2, "layers", 11.1], [["Application Support", 27.5, "folder"], ["Caches", 11.1, "folder", 11.1], ["Containers", 14.8, "folder"], ["Mail", 9.8, "mail"]]],
  [["Movies", 41.7, "video"], [["Final Cut", 26.3, "folder"], ["Screen Captures", 15.4, "folder"]]],
  [["Pictures", 33.5, "photo"], [["Photos Library", 30.2, "folder"], ["Exports", 3.3, "folder"]]],
  [["Downloads", 29.3, "down", 12.4], [["Blender-4.3.dmg", 9.6, "chip", 9.6], ["Install macOS Ventura.dmg", 5.1, "down", 2.8], ["Everything else", 14.6, "folder"]]],
  [["Documents", 21.8, "doc"], [["Clients", 14.9, "folder"], ["Taxes", 6.9, "folder"]]],
  [["Dropbox", 15.2, "cloud"]],
  [["Music", 12.6, "music"]],
  [["Sites", 7.9, "folder"]],
  [["Desktop", 3.1, "folder"]],
];

function homeTree(): SpaceNode {
  const children = HOME_TREE.map(([leaf, kids]) => folderNode(HOME, leaf, kids));
  return { name: "maya", path: HOME, kind: "folder", icon: "user", size: children.reduce((a, c) => a + c.size, 0), reclaimable: children.reduce((a, c) => a + c.reclaimable, 0), children };
}

function volumeTree(mount: string): SpaceNode {
  const system = mount === "/";
  const parts: Leaf[] = system
    ? [["Users", 301 - 77, "user", 12], ["Applications", 38, "apps"], ["System", 21, "chip"], ["Library", 12, "layers"], ["private", 6, "lock"]]
    : [["Backups", 760, "history"], ["Footage", 290, "video"], ["Archive", 130, "folder"]];
  const children = parts.map((p) => folderNode(system ? "" : mount, p));
  return { name: system ? "Macintosh HD" : "Studio Drive", path: mount, kind: "folder", icon: "folder", size: children.reduce((a, c) => a + c.size, 0), reclaimable: children.reduce((a, c) => a + c.reclaimable, 0), children };
}

// ---- files ----
const bigFile = (path: string, mib: number, changedDays: number, openedDays: number, kind: LargeFile["kind"]): LargeFile => ({
  path, name: path.slice(path.lastIndexOf("/") + 1), size: Math.round(mib * MIB), modified: daysAgo(changedDays), accessed: daysAgo(openedDays), kind,
});
const BIG_FILES: LargeFile[] = [
  bigFile(`${HOME}/Movies/Final Cut/Brand film - final cut.mov`, 11300, 350, 340, "video"),
  bigFile(`${HOME}/Downloads/Blender-4.3.dmg`, 9830, 60, 60, "disk-image"),
  bigFile(`${HOME}/Downloads/Install macOS Ventura.dmg`, 5240, 410, 410, "disk-image"),
  bigFile(`${HOME}/Movies/Screen Captures/Onboarding walkthrough.mov`, 3770, 48, 41, "video"),
  bigFile(`${HOME}/Documents/Clients/Project-archive-2021.zip`, 3290, 880, 860, "archive"),
  bigFile(`${HOME}/Music/Sessions/Album mixdown (all stems).aif`, 2410, 275, 262, "audio"),
  bigFile(`${HOME}/Pictures/Exports/wedding-gallery-full.zip`, 1960, 190, 189, "archive"),
  bigFile(`${HOME}/Downloads/Docker.dmg`, 1180, 130, 130, "disk-image"),
  bigFile(`${HOME}/Developer/Simulators/watchOS-runtime.dmg`, 1040, 220, 215, "disk-image"),
  bigFile(`${HOME}/Documents/Taxes/receipts-scan.pdf`, 790, 690, 600, "document"),
  bigFile(`${HOME}/Downloads/Adobe-Creative-Cloud-Installer.dmg`, 640, 95, 95, "disk-image"),
  bigFile("/Volumes/Studio/Footage/Festival day 2 (4K).mov", 14900, 520, 505, "video"),
  bigFile("/Volumes/Studio/Backups/ubuntu-24.04-desktop-amd64.iso", 8120, 760, 740, "disk-image"),
  bigFile("/Volumes/Studio/Archive/raw-photos-2019.zip", 4460, 1100, 1090, "archive"),
];

// what the demo says the safety rules kept out, so the quiet line can be seen without a real scan
const HIDDEN_BIG_FILES = 12;
const HIDDEN_COPIES = 3;

const dupGroup = (hash: string, mib: number, copies: [string, number][]): DuplicateGroup => ({
  hash, size: Math.round(mib * MIB), files: copies.map(([path, age]) => ({ path, modified: daysAgo(age) })),
});
const DUP_GROUPS: DuplicateGroup[] = [
  dupGroup("3c91fa", 1260, [[`${HOME}/Downloads/Festival teaser.mp4`, 300], [`${HOME}/Movies/Festival teaser.mp4`, 140], ["/Volumes/Studio/Footage/Festival teaser.mp4", 139]]),
  dupGroup("7be024", 355, [[`${HOME}/Documents/Clients/Pitch deck v3.pdf`, 45], [`${HOME}/Desktop/Pitch deck v3 copy.pdf`, 44]]),
  dupGroup("a98d31", 214, [[`${HOME}/Pictures/Exports/site-mockup.png`, 12], [`${HOME}/Downloads/site-mockup.png`, 13], [`${HOME}/Sites/assets/site-mockup.png`, 70]]),
  dupGroup("d40e6c", 142, [[`${HOME}/Music/Sessions/Demo take 2.wav`, 360], [`${HOME}/Downloads/Demo take 2.wav`, 355]]),
  dupGroup("f13b88", 88, [[`${HOME}/Documents/Taxes/invoice-2023-114.pdf`, 520], [`${HOME}/Downloads/invoice-2023-114 (1).pdf`, 515]]),
  dupGroup("19ac47", 57, [[`${HOME}/Sites/assets/logo-large.psd`, 33], [`${HOME}/Desktop/logo-large.psd`, 34], [`${HOME}/Downloads/logo-large-final.psd`, 30]]),
];

type AppRow = [name: string, maker: string, version: string, mib: number, usedDaysAgo: number | null, builtIn?: boolean];
const APP_ROWS: AppRow[] = [
  ["Final Cut Pro", "Apple", "10.8.1", 8900, 17], ["Logic Pro", "Apple", "11.1", 6800, 48], ["Blender", "Blender Foundation", "4.3.2", 4100, 150],
  ["Affinity Photo 2", "Serif", "2.5", 2300, 9], ["Sketch", "Sketch B.V.", "101.1", 1100, 3], ["Microsoft Excel", "Microsoft", "16.93", 2700, 70],
  ["Slack", "Slack Technologies", "4.42", 780, 0], ["Firefox", "Mozilla", "134.0", 560, 2], ["Arc", "The Browser Company", "1.78", 620, 0],
  ["Obsidian", "Obsidian", "1.7.7", 480, 1], ["Spotify", "Spotify", "1.2.54", 530, 4], ["Discord", "Discord Inc.", "0.0.332", 470, 12],
  ["Steam", "Valve", "1747", 420, 270], ["Notion", "Notion Labs", "4.4", 330, 25], ["1Password", "AgileBits", "8.10", 260, 0],
  ["Raycast", "Raycast", "1.88", 190, 0], ["Pixelmator Pro", "Pixelmator", "3.6", 640, 210], ["Transmit", "Panic", "5.10", 120, 330],
  ["Safari", "Apple", "18.3", 450, 0, true], ["Mail", "Apple", "16.0", 60, 3, true], ["Photos", "Apple", "10.0", 120, 8, true],
  ["Messages", "Apple", "14.0", 18, 0, true], ["Preview", "Apple", "11.0", 14, 1, true], ["Notes", "Apple", "4.11", 22, 0, true],
];
const APPS: InstalledApp[] = APP_ROWS.map(([name, publisher, version, mib, used, builtIn]) => ({
  id: `/Applications/${name}.app`, path: `/Applications/${name}.app`, iconPath: `/Applications/${name}.app`, name, publisher, version,
  size: Math.round(mib * MIB), installed: daysAgo(500), canUninstall: !builtIn, ...(used === null ? {} : { lastUsed: daysAgo(used) }),
}));

type LoginRow = [id: string, name: string, command: string, where: string, on: boolean, impact: StartupItem["impact"], admin?: boolean];
const LOGIN_ROWS: LoginRow[] = [
  ["slack", "Slack", "/Applications/Slack.app/Contents/MacOS/Slack -u", "Login Items", true, "high"],
  ["teams", "Microsoft Teams", "/Applications/Microsoft Teams.app/Contents/MacOS/MSTeams", "Login Items", true, "high"],
  ["adobe", "Adobe Creative Cloud", "/Library/Application Support/Adobe/OOBE/Creative Cloud Helper.app", "/Library/LaunchAgents", true, "high", true],
  ["dropbox", "Dropbox", "/Applications/Dropbox.app/Contents/MacOS/Dropbox --headless", "LaunchAgents", true, "medium"],
  ["steam", "Steam", "/Applications/Steam.app/Contents/MacOS/steam_osx -silent", "Login Items", false, "medium"],
  ["onepassword", "1Password", "/Applications/1Password.app/Contents/MacOS/1Password", "Login Items", true, "low"],
  ["raycast", "Raycast", "/Applications/Raycast.app/Contents/MacOS/Raycast", "Login Items", true, "low"],
  ["google-keystone", "Google Software Update", "/Library/Google/GoogleSoftwareUpdate/GoogleSoftwareUpdateAgent.app", "/Library/LaunchAgents", true, "low", true],
];
let loginItems: StartupItem[] = LOGIN_ROWS.map(([id, name, command, location, enabled, impact, admin]) => ({
  id, name, command, location, enabled, impact, canToggle: true, needsAdmin: !!admin,
}));

const PROTECTION: ProtectionStatus = {
  provider: "XProtect + Gatekeeper", enabled: true, realtime: true, firewall: false, definitionsUpdated: Date.now() - 9 * HOUR_MS,
  lastScan: daysAgo(6), score: 87, notes: ["The application firewall is switched off. Turning it on blocks unsolicited incoming connections."],
};

export function createMockApi(): BersihinApi {
  const subscribers = new Set<(p: ScanProgress) => void>();
  const jobs = new Map<string, { task: ScanProgress["task"]; stop: boolean }>();
  const volumes = VOLUMES.map((v) => ({ ...v }));
  const swept = new Set<string>();
  const binned = new Set<string>();
  let deniedShown = false;
  let prefs: Settings = { aiProvider: "claude", hasClaudeKey: false, hasOpenAiKey: false, claudeCompat: { baseUrl: "", model: "", hasKey: false }, openAiCompat: { baseUrl: "", model: "", hasKey: false }, background: "mist", largeFileMinMb: 500, confirmBeforeClean: true };

  // Like Go: every scan is remembered by its run id for a while, and a clean, trash or uninstall may only name
  // things that scan returned.
  const scans = new Map<string, { at: number; items: Set<string> }>();
  const begin = (runId: string) => {
    if (!RUN_ID_PATTERN.test(runId)) throw new Error("Invalid request");
    if (jobs.has(runId) || scans.has(runId)) throw new Error("Invalid request: run id in use");
  };
  const remember = (runId: string, items: Iterable<string>) => void scans.set(runId, { at: Date.now(), items: new Set(items) });
  const recall = (scanId: string) => {
    const scan = scans.get(scanId);
    if (!scan || Date.now() - scan.at >= SCAN_TTL_MS) {
      scans.delete(scanId);
      throw new Error("Scan expired: scan again");
    }
    return scan.items;
  };
  const noResult = (): CleanResult => ({ freed: 0, removed: 0, failed: 0, denied: 0, skipped: [], notes: [], cancelled: false });

  /** Reports each phase in turn, spreading `ms` over them; throws once cancelScan was called. */
  const walk = async (runId: string, task: ScanProgress["task"], steps: [phase: string, current?: string][], ms: number) => {
    begin(runId);
    const job = { task, stop: false };
    jobs.set(runId, job);
    try {
      let n = 0;
      for (const [phase, current] of steps) {
        n += 1;
        const report: ScanProgress = { id: runId, task, phase, done: n, total: steps.length + 1, ...(current ? { current } : {}) };
        subscribers.forEach((fn) => fn(report));
        await pause(ms / steps.length);
        if (job.stop) throw new Error("Scan cancelled");
      }
    } finally {
      jobs.delete(runId);
      runEnded(runId);
    }
  };

  const bytesAt = (path: string) =>
    BIG_FILES.find((f) => f.path === path)?.size ?? DUP_GROUPS.find((g) => g.files.some((f) => f.path === path))?.size ?? 0;
  const release = (bytes: number) => {
    const sys = volumes[0]!;
    sys.used = Math.max(0, sys.used - bytes);
    sys.free = sys.total - sys.used;
  };

  return {
    system: async () => (await pause(120), MACHINE),
    disk: async () => (await pause(120), { ...volumes[0]! }),
    disks: async () => (await pause(160), volumes.map((v) => ({ ...v }))),
    fileIcons: async () => ({}),

    scanJunk: async (runId) => {
      await walk(runId, "junk", JUNK.map((c) => [c.name, c.locations[0]]), 3200);
      const found = JUNK.map((c) => (swept.has(c.id) ? { ...c, size: 0, count: 0 } : { ...c }));
      remember(runId, found.map((c) => c.id));
      return found;
    },
    cleanJunk: async (scanId, ids) => {
      const known = recall(scanId);
      const unknown = ids.filter((id) => !known.has(id));
      const todo = JUNK.filter((c) => ids.includes(c.id) && known.has(c.id) && !swept.has(c.id));
      const runId = `mock-clean-${Math.random().toString(36).slice(2, 12)}-${Date.now()}`;
      try {
        await walk(runId, "clean", todo.map((c) => [c.name]), 1800);
      } catch {
        return { ...noResult(), cancelled: true };
      }
      todo.forEach((c) => swept.add(c.id));
      const freed = todo.reduce((a, c) => a + c.size, 0);
      release(freed);
      // the Trash category shows, once, what a folder macOS protects looks like
      const denied = !deniedShown && todo.some((c) => c.id === "trash") ? 1 : 0;
      if (denied) deniedShown = true;
      return {
        ...noResult(), freed, denied,
        removed: todo.reduce((a, c) => a + c.count, 0),
        failed: unknown.length,
        skipped: unknown.length ? [`${unknown.length} ${unknown.length === 1 ? "item was" : "items were"} not part of that scan`] : [],
        notes: todo.some((c) => c.id === "old-downloads") ? [`Old downloads: ${JUNK.find((c) => c.id === "old-downloads")!.count} items went to the Trash`] : [],
      };
    },
    scanSpaces: async (runId, root) => {
      const base = root ?? HOME;
      await walk(runId, "spaces", [["Library", `${base}/Library`], ["Developer", `${base}/Developer`], ["Movies", `${base}/Movies`], ["Pictures", `${base}/Pictures`]], root ? 3200 : 2600);
      return root ? volumeTree(root) : homeTree();
    },
    scanLargeFiles: async (runId, minBytes) => {
      await walk(runId, "large", [["Documents", `${HOME}/Documents`], ["Downloads", `${HOME}/Downloads`], ["Movies", `${HOME}/Movies`], ["Studio Drive", "/Volumes/Studio"]], 3400);
      const found = BIG_FILES.filter((f) => f.size >= minBytes && !binned.has(f.path)).map((f) => ({ ...f }));
      remember(runId, found.map((f) => f.path));
      return { files: found, hidden: HIDDEN_BIG_FILES };
    },
    scanDuplicates: async (runId) => {
      await walk(runId, "dupes", [["Grouping by size"], ["Checking the first bytes"], ["Comparing whole files"]], 4500);
      const found = DUP_GROUPS.map((g) => ({ ...g, files: g.files.filter((f) => !binned.has(f.path)).map((f) => ({ ...f })) })).filter((g) => g.files.length > 0);
      remember(runId, found.flatMap((g) => g.files.map((f) => f.path)));
      return { groups: found, hidden: HIDDEN_COPIES };
    },
    trashPaths: async (scanId, paths) => {
      const known = recall(scanId);
      const runId = `mock-trash-${Math.random().toString(36).slice(2, 12)}-${Date.now()}`;
      const job = { task: "trash" as const, stop: false };
      jobs.set(runId, job);
      const out = noResult();
      let unknown = 0;
      let at = 0;
      try {
        for (const p of paths) {
          if (job.stop) break;
          at += 1;
          subscribers.forEach((fn) => fn({ id: runId, task: "trash", phase: "Moving to the Trash", done: at, total: paths.length + 1, current: p }));
          await pause(Math.min(450, 4000 / Math.max(1, paths.length)));
          if (!known.has(p)) {
            unknown += 1;
            continue;
          }
          binned.add(p);
          out.freed += bytesAt(p);
          out.removed += 1;
        }
      } finally {
        jobs.delete(runId);
        runEnded(runId);
      }
      const left = paths.length - at;
      if (job.stop) {
        out.cancelled = true;
        out.failed += left;
        if (left > 0) out.skipped.push(`Stopped early: ${left} not moved`);
      }
      if (unknown > 0) out.skipped.push(`${unknown} ${unknown === 1 ? "item was" : "items were"} not part of that scan`);
      release(out.freed);
      return out;
    },
    revealPath: async () => pause(80),

    startupItems: async () => (await pause(700), loginItems.map((i) => ({ ...i }))),
    setStartupItem: async (id, enabled) => {
      await pause(loginItems.find((i) => i.id === id)?.needsAdmin ? 1400 : 500);
      loginItems = loginItems.map((i) => (i.id === id ? { ...i, enabled } : i));
      return loginItems.map((i) => ({ ...i }));
    },
    installedApps: async (runId) => {
      await walk(runId, "apps", [["Reading /Applications", "/Applications"]], 1600);
      remember(runId, APPS.map((a) => a.id));
      return APPS.map((a) => ({ ...a }));
    },
    uninstallApp: async (scanId, id) => {
      if (!recall(scanId).has(id)) throw new Error("Invalid request");
      await pause(900);
      const at = APPS.findIndex((a) => a.id === id);
      const label = APPS[at]?.name ?? "The app";
      if (at >= 0) APPS.splice(at, 1);
      return { ok: true, message: `${label} moved to the Trash` };
    },
    protection: async () => (await pause(900), { ...PROTECTION, notes: [...PROTECTION.notes] }),

    getSettings: async () => (await pause(100), { ...prefs }),
    setSettings: async (patch) => {
      await pause(150);
      // Like Go: an address is normalised, and another host (or none) deletes that kind's saved key.
      const merge = (cur: AiEndpoint, p: AiEndpointPatch | undefined): AiEndpoint => {
        if (!p) return cur;
        const next = { ...cur };
        if (p.baseUrl !== undefined) {
          next.baseUrl = p.baseUrl.trim() === "" ? "" : normalizeUrl(p.baseUrl);
          if (cur.baseUrl && next.baseUrl !== cur.baseUrl && hostChanged(cur.baseUrl, next.baseUrl)) next.hasKey = false;
        }
        if (p.model !== undefined) {
          const model = p.model.trim();
          const bad = model === "" ? "" : checkModel(model);
          if (bad) throw new Error(bad);
          next.model = model;
        }
        return next;
      };
      const { claudeCompat, openAiCompat, ...rest } = patch;
      prefs = { ...prefs, ...rest, claudeCompat: merge(prefs.claudeCompat, claudeCompat), openAiCompat: merge(prefs.openAiCompat, openAiCompat) };
      return { ...prefs };
    },
    setApiKey: async (provider, key) => {
      await pause(400);
      if (provider === "claude-compatible" || provider === "openai-compatible") {
        const field = provider === "claude-compatible" ? "claudeCompat" : "openAiCompat";
        prefs = { ...prefs, [field]: { ...prefs[field], hasKey: key !== null } };
      } else {
        prefs = { ...prefs, [provider === "claude" ? "hasClaudeKey" : "hasOpenAiKey"]: key !== null };
      }
      return { ...prefs };
    },
    testConnection: async (provider: AiProvider, draft: AiEndpointPatch | null): Promise<AiTestResult> => {
      await pause(600);
      const saved = provider === "claude-compatible" ? prefs.claudeCompat : provider === "openai-compatible" ? prefs.openAiCompat : null;
      if (!saved) return { ok: true, code: "ok", message: "Connected", detail: "200 · api.anthropic.com/v1/messages", mode: "structured", ms: 420 };
      const url = draft?.baseUrl ?? saved.baseUrl;
      const model = draft?.model ?? saved.model;
      const fail = (code: string, message: string, detail?: string): AiTestResult => ({ ok: false, code, message, ...(detail ? { detail } : {}), ms: 600 });
      try {
        if (!url.trim()) return fail("bad_url", "Enter the address of your service first.");
        const draftUrl = normalizeUrl(url);
        if (saved.baseUrl && hostChanged(saved.baseUrl, draftUrl)) return fail("bad_url", "Save the new address first, then test the connection.");
        if (!model.trim()) return fail("no_model", "Enter a model name first.");
        if (!saved.hasKey) return fail("no_key", "Add an API key first.");
        if (/rebind/.test(draftUrl)) return fail("no_key", "The saved API key was not saved for this address. Save the API key again in Settings.");
        const at = new URL(draftUrl);
        const route = `${at.host}${at.pathname}`;
        if (/localhost|openrouter/.test(draftUrl)) return { ok: true, code: "ok", message: "Connected", detail: `200 · ${route}`, mode: "structured", ms: 420 };
        return fail("not_found", "The service answered, but nothing lives at that address. Check the path, and whether it needs /v1.", `404 · ${route}`);
      } catch (e) {
        return fail("bad_url", e instanceof Error ? e.message : "Check the address.");
      }
    },
    askAi: async (question: string, context: AiContext): Promise<AiAdvice> => {
      await pause(1600);
      if (isCompatible(prefs.aiProvider) && !isReady(prefs, prefs.aiProvider))
        throw new Error("Set up your provider in Settings first: add its address, model and key.");
      const stale = [...(context.largeFiles ?? [])].sort((a, b) => a.modified - b.modified);
      const advice: AiAdvice["suggestions"] = stale.slice(0, 2).map((f) => ({
        path: f.path, verdict: "safe", reason: "Big, untouched for a long while, and an installer or disk image you can download again.",
      }));
      if (stale[2]) advice.push({ path: stale[2].path, verdict: "review", reason: "Looks like your own work. Confirm a backup exists before deleting." });
      advice.push({ path: "~/Library/Caches", verdict: "safe", reason: "Apps recreate these. The Cleanup screen removes them safely." });
      // Like ParseAdvice in Go: "safe" survives only for a junk category or location the assistant was shown.
      const known = new Set((context.junk ?? []).flatMap((j) => [j.name, ...j.locations]).map((x) => x.toLowerCase()));
      advice.forEach((a) => {
        if (a.verdict === "safe" && !known.has(a.path.toLowerCase())) a.verdict = "review";
      });
      return {
        provider: prefs.aiProvider,
        answer: `Demo reply to "${question}". Your space goes mostly to Developer, Library and Downloads. These are the quickest wins, and nothing is deleted until you approve.`,
        suggestions: advice,
      };
    },
    openExternal: async () => pause(50),

    cancelScan: async (tasks) => {
      jobs.forEach((j) => {
        if (!tasks || tasks.includes(j.task)) j.stop = true;
      });
    },
    onProgress: (cb) => {
      subscribers.add(cb);
      return () => void subscribers.delete(cb);
    },
    onWindowFocus: () => () => undefined,
  };
}
