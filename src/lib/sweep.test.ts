import { describe, expect, test } from "bun:test";
import { runSweep, type SweepDeps } from "./sweep";
import type { CleanResult } from "../types";

const NOW = 10_000_000;
const ok = (over: Partial<CleanResult> = {}): CleanResult => ({ freed: 10, removed: 1, failed: 0, denied: 0, skipped: [], notes: [], cancelled: false, ...over });

function setup(clean: SweepDeps["clean"], scanId: string | null = "scan-1", scanAge = 1000) {
  const log: string[] = [];
  const notices: { text: string; tone: string }[] = [];
  const ends: { ok: boolean; partial: boolean }[] = [];
  const deps: SweepDeps = {
    scanId,
    scannedAt: NOW - scanAge,
    now: () => NOW,
    clean: async (s, ids) => {
      log.push(`clean:${s}:${ids.join(",")}`);
      return clean(s, ids);
    },
    finish: (e) => (log.push("finish"), void ends.push(e)),
    afterChange: () => void log.push("forget"),
    rescan: async () => void log.push("rescan"),
    notify: (text, tone) => void notices.push({ text, tone }),
  };
  return { deps, log, notices, ends };
}

describe("runSweep rescans after every outcome (H2/H3)", () => {
  const outcomes: [string, SweepDeps["clean"]][] = [
    ["clean", async () => ok()],
    ["partial", async () => ok({ failed: 2 })],
    ["rejected", async () => { throw new Error("boom"); }],
    ["cancelled result", async () => ok({ cancelled: true })],
    ["cancel error", async () => { throw new Error("scan cancelled"); }],
    ["expired scan", async () => { throw new Error("Scan expired: scan again"); }],
  ];
  for (const [name, fn] of outcomes) {
    test(`${name}: forgets affected scans and rescans, after the activity is released`, async () => {
      const { deps, log } = setup(fn);
      await runSweep(["a"], deps);
      expect(log.filter((l) => l === "rescan")).toHaveLength(1);
      expect(log.filter((l) => l === "forget")).toHaveLength(1);
      expect(log.indexOf("finish")).toBeLessThan(log.indexOf("rescan"));
    });
  }
});

describe("runSweep outcomes", () => {
  test("sends the scan id and ids to clean", async () => {
    const { deps, log } = setup(async () => ok());
    await runSweep(["a", "b"], deps);
    expect(log[0]).toBe("clean:scan-1:a,b");
  });

  test("notes alone do not make it partial (M5)", async () => {
    const { deps, ends } = setup(async () => ok({ notes: ["Old downloads: x"] }));
    await runSweep(["a"], deps);
    expect(ends).toEqual([{ ok: true, partial: false }]);
  });

  test("denied, failed or skipped make it partial; a cancelled result too (nothing is zeroed)", async () => {
    for (const over of [{ denied: 1 }, { failed: 1 }, { skipped: ["x"] }, { cancelled: true }]) {
      const { deps, ends } = setup(async () => ok(over));
      await runSweep(["a"], deps);
      expect(ends[0]).toEqual({ ok: true, partial: true });
    }
  });

  test("returns the result of a clean", async () => {
    const result = ok({ freed: 99 });
    expect(await runSweep(["a"], setup(async () => result).deps)).toBe(result);
  });

  test("a cancel is not reported as 'Cleaning failed' (H2)", async () => {
    const { deps, notices, ends } = setup(async () => { throw new Error("scan cancelled"); });
    expect(await runSweep(["a"], deps)).toBeNull();
    expect(notices).toHaveLength(1);
    expect(notices[0]!.text).not.toContain("failed");
    expect(notices[0]!.text).toContain("stopped");
    expect(ends).toEqual([{ ok: false, partial: false }]);
  });

  test("an expired scan asks to review again", async () => {
    const { deps, notices } = setup(async () => { throw new Error("Scan expired: scan again"); });
    await runSweep(["a"], deps);
    expect(notices).toEqual([{ text: "Results were out of date. Review and clean again", tone: "err" }]);
  });

  test("with no scan id nothing is sent to Go", async () => {
    const { deps, log, notices } = setup(async () => ok(), null);
    expect(await runSweep(["a"], deps)).toBeNull();
    expect(log.some((l) => l.startsWith("clean"))).toBe(false);
    expect(notices[0]!.text).toContain("out of date");
    expect(log).toContain("rescan");
  });

  test("another failure shows its own message, or the fallback", async () => {
    const a = setup(async () => { throw new Error("Confirmation expired"); });
    await runSweep(["a"], a.deps);
    expect(a.notices[0]).toEqual({ text: "Confirmation expired", tone: "err" });
    const b = setup(async () => { throw "weird"; });
    await runSweep(["a"], b.deps);
    expect(b.notices[0]).toEqual({ text: "Cleaning failed", tone: "err" });
  });

  test("a scan older than the page limit is not cleaned: it rescans and says the results are out of date (H1)", async () => {
    const { deps, log, notices } = setup(async () => ok(), "scan-1", 9 * 60_000);
    expect(await runSweep(["a"], deps)).toBeNull();
    expect(log.some((l) => l.startsWith("clean"))).toBe(false);
    expect(notices).toEqual([{ text: "Results were out of date. Review and clean again", tone: "err" }]);
    expect(log).toContain("rescan");
  });

  test("a scan just inside the limit is cleaned", async () => {
    const { deps, log } = setup(async () => ok(), "scan-1", 9 * 60_000 - 1);
    await runSweep(["a"], deps);
    expect(log[0]).toBe("clean:scan-1:a");
  });

  test("the full after-change step runs (maps marked stale, disks read again), not only forgetting scans (H3)", async () => {
    const { deps, log } = setup(async () => ok());
    await runSweep(["a"], deps);
    expect(log).toContain("forget");
  });
});

