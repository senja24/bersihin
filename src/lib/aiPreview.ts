import type { AiContext } from "../types";
import { isWinPath, within } from "./platform";

const DATA_FIRMLINK = /^\/System\/Volumes\/Data(?=\/|$)/;
const stripFirmlink = (p: string) => p.replace(DATA_FIRMLINK, "") || "/";
/** Any user folder, the person's own or not, loses its name. */
const hideUser = (p: string) => p.replace(/^\/Users\/[^/]+/, "/Users/…");

/**
 * What the assistant may see of a path: the home folder as ~, a mounted volume as its name plus the
 * relative path, and never a user name: other users' folders (also on other volumes, and in the
 * /System/Volumes/Data form) become /Users/…. Works the same when `home` is still unknown ("").
 */
export function shortenPath(path: string, home: string): string {
  if (isWinPath(path) || isWinPath(home)) return shortenWinPath(path, home);
  const p = stripFirmlink(path);
  const base = stripFirmlink(home).replace(/\/+$/, "");
  if (base && (p === base || p.startsWith(base + "/"))) return "~" + p.slice(base.length);
  const volume = /^\/Volumes\/([^/]+)(\/.*)?$/.exec(p);
  if (volume) return volume[1]! + hideUser((volume[2] ?? "").replace(/\/+$/, ""));
  return hideUser(p);
}

/**
 * shortenPath for Windows: the profile as ~\ (names compare without case), and any other profile folder, on any
 * drive, as X:\Users\….
 */
function shortenWinPath(path: string, home: string): string {
  const p = path.replace(/\//g, "\\");
  const base = home.replace(/\//g, "\\").replace(/\\+$/, "");
  if (base && within(base, p)) return "~" + p.slice(base.length);
  return p.replace(/^([A-Za-z]:\\Users\\)[^\\]+/i, "$1…");
}

/** Adds mappings from a shortened path to the real one; a short form that maps to two real paths becomes null (ambiguous). */
export function mergeOriginals(target: Map<string, string | null>, incoming: Map<string, string | null>): void {
  incoming.forEach((real, short) => {
    const known = target.get(short);
    target.set(short, known === undefined || known === real ? real : null);
  });
}

/**
 * Shortens every path in the context. `originals` maps each shortened file path and junk location back to the
 * real one (null when two real paths share a short form); `files` holds the shortened large-file paths.
 */
export function shortenContext(context: AiContext, home: string): { context: AiContext; originals: Map<string, string | null>; files: Set<string> } {
  const originals = new Map<string, string | null>();
  const files = new Set<string>();
  const short = (real: string) => {
    const path = shortenPath(real, home);
    mergeOriginals(originals, new Map([[path, real]]));
    return path;
  };
  const out: AiContext = { ...context };
  if (context.junk) out.junk = context.junk.map((j) => ({ ...j, locations: j.locations.map(short) }));
  if (context.largeFiles) {
    out.largeFiles = context.largeFiles.map((f) => {
      const path = short(f.path);
      files.add(path);
      return { ...f, path };
    });
  }
  return { context: out, originals, files };
}

/** The first send of a conversation, and the first one after switching vendor, is held until the user has seen the data and agreed. */
export const needsPreview = (approvedFor: string | null, vendor: string): boolean => approvedFor !== vendor;
