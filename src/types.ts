// Contract between the page and the backend. Names match the Go side's JSON,
// so do not rename them. Byte counts for sizes, epoch milliseconds for times.

export type IconName =
  | "scan" | "broom" | "shield" | "gauge" | "apps" | "bubbles" | "file" | "dupes" | "history" | "assist"
  | "user" | "game" | "down" | "chip" | "more" | "check" | "arrow" | "heart" | "chat" | "gear"
  | "trash" | "folder" | "photo" | "video" | "music" | "doc" | "code" | "cloud" | "mail" | "globe"
  | "layers" | "lock" | "power" | "search" | "close" | "plus" | "reveal" | "spark" | "warning" | "clock";

export type SystemInfo = {
  platform: "darwin";
  arch: string;
  osVersion: string;
  appVersion: string;
  homeDir: string;
};

export type DiskInfo = {
  mount: string; // "/" or "/Volumes/Backup"
  label: string; // "Macintosh HD"
  total: number;
  free: number;
  used: number;
  /** the volume macOS (and the home folder) lives on */
  system: boolean;
};

/** Names of the long operations that report progress and can be cancelled. */
export type ScanTask = "junk" | "spaces" | "large" | "dupes" | "clean" | "apps" | "trash";

export type ScanProgress = {
  /** the run id the page chose for the call */
  id: string;
  task: ScanTask;
  phase: string; // human readable, e.g. "Browser caches"
  done: number;
  total: number; // 0 when unknown
  current?: string; // path being looked at
};

/** One kind of removable junk, such as the browser caches. */
export type JunkCategory = {
  id: string;
  name: string;
  description: string;
  icon: IconName;
  size: number;
  count: number; // number of top-level entries that would be removed
  locations: string[]; // display paths (with ~ for home)
  safe: boolean; // true = selected by default
  /** true = deleted for good (caches, logs, the Trash); false = moved to the Trash */
  irreversible: boolean;
};

export type CleanResult = {
  freed: number;
  removed: number;
  /** items that were in use, blocked or could not be moved; does not include denied */
  failed: number;
  /** files and folders macOS did not let us read or delete */
  denied: number;
  /** real problems only ("12 files in use") */
  skipped: string[];
  /** informational only ("Old downloads: 87 items went to the Trash"), never a problem */
  notes: string[];
  /** the user stopped the operation before it finished */
  cancelled: boolean;
};

export type UninstallResult = { ok: boolean; message: string };

export type SpaceNode = {
  name: string;
  path: string;
  size: number;
  kind: "folder" | "file" | "other";
  icon: IconName;
  reclaimable: number; // bytes inside that are junk / old downloads
  children?: SpaceNode[];
};

export type LargeFile = {
  path: string;
  name: string;
  size: number;
  modified: number;
  accessed: number;
  kind: "video" | "archive" | "disk-image" | "installer" | "audio" | "photo" | "document" | "other";
};

export type LargeScan = {
  files: LargeFile[];
  hidden: number; // big files the safety rules kept out of the list
};

type CopyRef = { path: string; modified: number };
type JunkDigest = { name: string; size: number; locations: string[] };
type FileDigest = { path: string; size: number; modified: number };
type FolderDigest = { name: string; size: number };
type DiskDigest = { total: number; free: number };

export type DuplicateGroup = {
  hash: string;
  size: number; // size of one copy
  files: CopyRef[];
};

export type DuplicateScan = {
  groups: DuplicateGroup[];
  hidden: number; // copies the safety rules kept out of the groups
};

export type StartupItem = {
  id: string;
  name: string;
  command: string;
  location: string; // "Login Items", "LaunchAgents"
  enabled: boolean;
  impact: "high" | "medium" | "low";
  canToggle: boolean;
  /** toggling needs administrator rights: macOS asks for the password */
  needsAdmin: boolean;
};

export type InstalledApp = {
  id: string;
  name: string;
  version?: string;
  publisher?: string;
  size?: number;
  installed?: number;
  lastUsed?: number;
  path?: string;
  /** file to take the app's real icon from (the .app bundle); pass it to fileIcons() */
  iconPath?: string;
  canUninstall: boolean;
};

export type ProtectionStatus = {
  provider: string; // "XProtect + Gatekeeper"
  enabled: boolean;
  realtime: boolean | null;
  firewall: boolean | null;
  definitionsUpdated: null | number;
  lastScan: number | null;
  score: number; // 0..100
  notes: string[];
};

export type AiProvider = "claude" | "chatgpt";
export type Background = "mist" | "grid" | "dots" | "grain";

export type Settings = {
  aiProvider: AiProvider;
  hasClaudeKey: boolean;
  hasOpenAiKey: boolean;
  background: Background;
  largeFileMinMb: number;
  confirmBeforeClean: boolean; // ask before deleting
};

/** The only data the assistant is given: names, paths, sizes. No file contents. */
export type AiContext = {
  junk?: JunkDigest[];
  largeFiles?: FileDigest[];
  spaces?: FolderDigest[];
  disk?: DiskDigest;
};

export type AiAdvice = {
  answer: string;
  suggestions: { path: string; verdict: "safe" | "review" | "keep"; reason: string }[];
  provider: AiProvider;
};

/** Every call is a round trip to the backend. */
export type BersihinApi = {
  system: () => Promise<SystemInfo>;
  disk: () => Promise<DiskInfo>;
  /** every mounted volume, the system volume first */
  disks: () => Promise<DiskInfo[]>;
  /** PNG data URLs of the system icons for the given paths, keyed by path; paths without an icon are omitted */
  fileIcons: (paths: string[]) => Promise<Record<string, string>>;
  /**
   * Every scan takes a run id the page chose (newRunId()). Progress events carry it, cancelScan stops by it, and it is
   * the scan id that cleanJunk, trashPaths and uninstallApp need.
   */
  scanJunk: (runId: string) => Promise<JunkCategory[]>;
  /** Asks Go for a confirmation token for exactly these ids, then cleans. Call only after the user confirmed. */
  cleanJunk: (scanId: string, ids: string[]) => Promise<CleanResult>;
  /** root = undefined for the home folder, or the mount of a volume from disks() */
  scanSpaces: (runId: string, root?: string) => Promise<SpaceNode>;
  /** scans the home folder plus every other mounted volume */
  scanLargeFiles: (runId: string, minBytes: number) => Promise<LargeScan>;
  scanDuplicates: (runId: string) => Promise<DuplicateScan>;
  /** scanId is the run id of the large files or duplicates scan the paths came from; asks for a token, then moves them. */
  trashPaths: (scanId: string, paths: string[]) => Promise<CleanResult>;
  revealPath: (path: string) => Promise<void>;
  startupItems: () => Promise<StartupItem[]>;
  setStartupItem: (id: string, enabled: boolean) => Promise<StartupItem[]>;
  installedApps: (runId: string) => Promise<InstalledApp[]>;
  /** scanId is the run id of the app list that id came from */
  uninstallApp: (scanId: string, id: string) => Promise<UninstallResult>;
  protection: () => Promise<ProtectionStatus>;
  getSettings: () => Promise<Settings>;
  setSettings: (patch: Partial<Omit<Settings, "hasClaudeKey" | "hasOpenAiKey">>) => Promise<Settings>;
  setApiKey: (provider: AiProvider, key: string | null) => Promise<Settings>;
  askAi: (question: string, context: AiContext) => Promise<AiAdvice>;
  openExternal: (url: string) => Promise<void>;
  /**
   * Stops the in-flight runs of the named tasks, or all of them when none are named. A stopped scan's promise rejects
   * with an error whose message contains "cancelled"; a stopped clean or trash resolves with `cancelled: true`.
   */
  cancelScan: (tasks?: ScanTask[]) => Promise<void>;
  /** Progress events from long scans; returns the unsubscribe function. */
  onProgress: (cb: (p: ScanProgress) => void) => () => void;
};
