import { describe, expect, test } from "bun:test";
import { cleanSummary, isPartial, trashSummary } from "./cleanSummary";
import type { CleanResult } from "../types";

const GB = 1024 ** 3;
const res = (over: Partial<CleanResult> = {}): CleanResult => ({ freed: GB, removed: 3, failed: 0, denied: 0, skipped: [], notes: [], cancelled: false, ...over });

describe("cleanSummary", () => {
  test("a plain clean says how much was freed", () => {
    expect(cleanSummary(res())).toBe("Freed 1.0 GB");
  });

  test("denied items are shown (S8)", () => {
    expect(cleanSummary(res({ denied: 3 }))).toContain("3 protected items left alone");
    expect(cleanSummary(res({ denied: 1 }))).toContain("1 protected item left alone");
  });

  test("failures and real skipped lines are shown", () => {
    expect(cleanSummary(res({ failed: 2 }))).toContain("2 in use, skipped");
    expect(cleanSummary(res({ skipped: ["4 items could not be cleaned", "x", "y"] }))).toBe("Freed 1.0 GB - 4 items could not be cleaned, x");
  });

  test("a note is shown only when nothing else needs saying (M5)", () => {
    const notes = ["Old downloads: 87 items went to the Trash"];
    expect(cleanSummary(res({ notes }))).toContain("Old downloads: 87 items went to the Trash");
    expect(cleanSummary(res({ notes, denied: 1 }))).not.toContain("Old downloads");
  });

  test("a stopped clean says stopped", () => {
    expect(cleanSummary(res({ cancelled: true }))).toMatch(/^Stopped/);
  });
});

describe("isPartial", () => {
  test("notes alone do not make a clean partial (M5)", () => {
    expect(isPartial(res({ notes: ["Old downloads: went to the Trash"] }))).toBe(false);
  });
  test("failed, denied or skipped do", () => {
    expect(isPartial(res({ failed: 1 }))).toBe(true);
    expect(isPartial(res({ denied: 1 }))).toBe(true);
    expect(isPartial(res({ skipped: ["x"] }))).toBe(true);
  });
});

describe("trashSummary", () => {
  test("counts moved items with the noun", () => {
    expect(trashSummary(res({ removed: 1 }), "file", "files")).toEqual({ text: "Moved 1 file (1.0 GB) to Trash", tone: "ok" });
    expect(trashSummary(res({ removed: 2 }), "copy", "copies").text).toStartWith("Moved 2 copies");
  });
  test("shows why files were left (H1 skipped lines) and denied", () => {
    const out = trashSummary(res({ removed: 1, skipped: ["3 files changed since the scan"], denied: 2 }), "file", "files");
    expect(out.text).toContain("3 files changed since the scan");
    expect(out.text).toContain("2 protected items left alone");
    expect(out.tone).toBe("ok");
  });
  test("moving nothing while something went wrong is an error", () => {
    expect(trashSummary(res({ removed: 0, freed: 0, skipped: ["1 file changed since the scan"] }), "file", "files").tone).toBe("err");
  });
  test("a stopped run says so", () => {
    expect(trashSummary(res({ removed: 1, cancelled: true }), "file", "files").text).toMatch(/^Stopped/);
  });
  test("a stopped run says so once, and counts what was not moved", () => {
    const text = trashSummary(res({ removed: 5, cancelled: true, failed: 9, skipped: ["Stopped early: 9 not moved"] }), "file", "files").text;
    expect(text.match(/Stopped/g)).toHaveLength(1);
    expect(text).toContain("9 not moved");
  });
});
