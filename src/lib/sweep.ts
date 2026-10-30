import type { CleanResult } from "../types";
import { isScanExpired } from "./confirm";
import { isPartial } from "./cleanSummary";
import { messageOf, wasCancelled } from "./format";
import { isExpired } from "./scanMemory";

export interface SweepDeps {
  /** The run id of the junk scan the ids come from; null when there is none. */
  scanId: string | null;
  clean: (scanId: string, ids: string[]) => Promise<CleanResult>;
  /** Called once the clean is over, before anything else, so the store can accept a rescan. */
  finish: (end: { ok: boolean; partial: boolean }) => void;
  /** When that scan finished (epoch ms); null when unknown. */
  scannedAt: number | null;
  now: () => number;
  /** The full step after files changed: forget remembered scans, mark space maps stale, read the disks again. */
  afterChange: () => void;
  rescan: () => Promise<void>;
  notify: (text: string, tone: "ok" | "err") => void;
}

const OUT_OF_DATE = "Results were out of date. Review and clean again";

/**
 * Cleans, then ALWAYS looks again, whatever happened: after an ok, partial, rejected, stopped or expired clean the
 * sizes on screen could be wrong. Returns the result when Go produced one, otherwise null (the toast is shown here).
 * A stopped result counts as partial so the categories are not shown as empty.
 */
export async function runSweep(ids: string[], d: SweepDeps): Promise<CleanResult | null> {
  let result: CleanResult | null = null;
  let problem: { text: string; tone: "ok" | "err" } | null = null;
  // the page lets go of a scan before Go does, so an old list is rescanned instead of offered for cleaning
  if (d.scanId === null || d.scannedAt === null || isExpired(d.scannedAt, d.now())) {
    problem = { text: OUT_OF_DATE, tone: "err" };
  } else {
    try {
      result = await d.clean(d.scanId, ids);
    } catch (err) {
      problem = failure(err);
    }
  }
  d.finish({ ok: result !== null, partial: result !== null && (isPartial(result) || result.cancelled) });
  d.afterChange();
  void d.rescan();
  if (problem) d.notify(problem.text, problem.tone);
  return result;
}

function failure(err: unknown): { text: string; tone: "ok" | "err" } {
  if (wasCancelled(err)) return { text: "Cleaning stopped", tone: "ok" };
  if (isScanExpired(err)) return { text: OUT_OF_DATE, tone: "err" };
  // a refused or expired confirmation reads as Go wrote it
  return { text: messageOf(err, "Cleaning failed"), tone: "err" };
}
