import type { CleanResult } from "../types";
import { formatBytes } from "./format";

/** Anything that stayed behind for a real reason. Notes are information only and never count. */
export const isPartial = (r: CleanResult) => r.failed > 0 || r.denied > 0 || r.skipped.length > 0;

const protectedLine = (n: number) => `${n} protected ${n === 1 ? "item" : "items"} left alone`;

/** Why some things stayed, in the order of importance; a note is added only when there is nothing else to say. */
function leftBehind(r: CleanResult, failedLine: string): string[] {
  // the head already says a stopped run was stopped; Go's own "Stopped early: N not moved" line would repeat it
  const lines = r.cancelled ? r.skipped.filter((l) => !/^stopped/i.test(l)) : r.skipped;
  const parts = lines.length ? [lines.slice(0, 2).join(", ")] : r.failed > 0 ? [failedLine] : [];
  if (r.denied > 0) parts.push(protectedLine(r.denied));
  if (parts.length === 0 && r.notes.length > 0) parts.push(r.notes[0]!);
  return parts;
}

/** The toast after a clean of junk categories. */
export function cleanSummary(r: CleanResult): string {
  const head = r.cancelled ? `Stopped early - freed ${formatBytes(r.freed)}` : `Freed ${formatBytes(r.freed)}`;
  return [head, ...leftBehind(r, `${r.failed} in use, skipped`)].join(" - ");
}

/** The toast after moving files to the Trash (bin: "Recycle Bin" on Windows), with its tone. */
export function trashSummary(r: CleanResult, one: string, many: string, bin = "Trash"): { text: string; tone: "ok" | "err" } {
  const moved = `${r.removed} ${r.removed === 1 ? one : many} (${formatBytes(r.freed)}) to ${bin === "Trash" ? "Trash" : `the ${bin}`}`;
  const head = r.cancelled ? `Stopped early - moved ${moved}` : `Moved ${moved}`;
  const text = [head, ...leftBehind(r, `${r.failed} ${r.cancelled ? "not moved" : "could not be moved"}`)].join(" - ");
  return { text, tone: r.removed === 0 && (isPartial(r) || r.cancelled) ? "err" : "ok" };
}
