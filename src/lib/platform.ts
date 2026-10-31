// What differs between macOS and Windows on the page: the words for the Trash, the file manager and the
// computer, the screens a platform does not have yet, and the path syntax. Go says which platform it runs on
// (System.info); until it has, and in the demo, the page is the Mac's.
import type { SystemInfo } from "../types";

/** A screen the page can hide when the backend says the platform does not have it. */
export type OptionalScreen = "applications" | "startup" | "protection";

export interface Platform {
  windows: boolean;
  /** "Trash" or "Recycle Bin" */
  trash: string;
  /** "Finder" or "File Explorer" */
  fileManager: string;
  /** "Mac" or "PC", as in "this Mac" */
  computer: string;
  /** "macOS" or "Windows" */
  os: string;
  /** screens the backend does not offer here */
  unsupported: ReadonlySet<string>;
}

export const MAC: Platform = { windows: false, trash: "Trash", fileManager: "Finder", computer: "Mac", os: "macOS", unsupported: new Set() };

export function platformOf(m: Pick<SystemInfo, "platform" | "trashName" | "fileManager" | "unsupported"> | null | undefined): Platform {
  if (!m) return MAC;
  const windows = m.platform === "windows";
  return {
    windows,
    trash: m.trashName || (windows ? "Recycle Bin" : "Trash"),
    fileManager: m.fileManager || (windows ? "File Explorer" : "Finder"),
    computer: windows ? "PC" : "Mac",
    os: windows ? "Windows" : "macOS",
    unsupported: new Set(m.unsupported ?? []),
  };
}

/**
 * A Windows path: a drive letter path (C:\Users\ika) or one Go wrote with the home folder as ~\. Mac paths keep
 * their own rules, where a backslash is an ordinary character of a name.
 */
export const isWinPath = (p: string) => /^[A-Za-z]:[\\/]/.test(p) || p.startsWith("~\\");

/** The separator of the path's own syntax. */
export const sepOf = (p: string) => (isWinPath(p) ? "\\" : "/");

const winParts = (p: string) => p.split(/[\\/]/).filter(Boolean);

/** The last element of a path. */
export function leafOf(path: string): string {
  return (isWinPath(path) ? winParts(path) : path.split("/").filter(Boolean)).pop() ?? path;
}

/** The folder holding a path: "/" for a top folder of a Mac volume, "C:\" for one of a Windows drive. */
export function parentOf(path: string): string {
  if (!isWinPath(path)) return path.replace(/\/[^/]*$/, "") || "/";
  const cut = path.replace(/[\\/]+$/, "").replace(/[\\/][^\\/]*$/, "");
  return /^[A-Za-z]:$/.test(cut) ? cut + "\\" : cut || path;
}

/**
 * Whether path is base or below it. Windows paths compare case-insensitively, either separator counting, as
 * NTFS does; Mac paths as written.
 */
export function within(base: string, path: string): boolean {
  if (!isWinPath(base) && !isWinPath(path)) return path === base || path.startsWith(base.replace(/\/+$/, "") + "/");
  const b = base.toLowerCase().replace(/\//g, "\\").replace(/\\+$/, "");
  const p = path.toLowerCase().replace(/\//g, "\\");
  return p === b || p.startsWith(b + "\\");
}
