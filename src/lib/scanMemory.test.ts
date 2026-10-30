import { describe, expect, test } from "bun:test";
import { SCAN_TTL_MS, createScanMemory, isExpired } from "./scanMemory";

function setup() {
  let now = 1_000_000;
  const mem = createScanMemory(() => now);
  return { mem, advance: (ms: number) => void (now += ms) };
}

describe("isExpired", () => {
  test("a scan is expired from 9 minutes on, so a long-open screen can check at action time", () => {
    expect(isExpired(1000, 1000 + SCAN_TTL_MS - 1)).toBe(false);
    expect(isExpired(1000, 1000 + SCAN_TTL_MS)).toBe(true);
    expect(isExpired(null, 5)).toBe(false);
  });
});

describe("scanMemory", () => {
  test("remembers data with its run id", () => {
    const { mem } = setup();
    mem.set("large:1", ["a"], "run-1");
    expect(mem.get("large:1")).toMatchObject({ data: ["a"], runId: "run-1" });
  });

  test("an answer older than the TTL is gone (H1); the TTL is 9 minutes", () => {
    const { mem, advance } = setup();
    expect(SCAN_TTL_MS).toBe(9 * 60_000);
    mem.set("k", 1, "r");
    advance(SCAN_TTL_MS - 1);
    expect(mem.has("k")).toBe(true);
    advance(1);
    expect(mem.has("k")).toBe(false);
    expect(mem.get("k")).toBeUndefined();
  });

  test("update keeps the run id and the age", () => {
    const { mem, advance } = setup();
    mem.set("k", [1, 2], "r");
    advance(60_000);
    mem.update("k", [1]);
    expect(mem.get("k")).toMatchObject({ data: [1], runId: "r" });
    advance(SCAN_TTL_MS - 60_000);
    expect(mem.has("k")).toBe(false);
  });

  test("update on a missing key stores nothing", () => {
    const { mem } = setup();
    mem.update("k", 1);
    expect(mem.has("k")).toBe(false);
  });

  test("forget drops every key with a prefix (H3) and bumps the epoch", () => {
    const { mem } = setup();
    mem.set("large:100", 1, "a");
    mem.set("large:500", 1, "b");
    mem.set("duplicates", 1, "c");
    mem.set("startup", 1, "d");
    const before = mem.epoch();
    mem.forget("large:", "duplicates");
    expect(mem.has("large:100") || mem.has("large:500") || mem.has("duplicates")).toBe(false);
    expect(mem.has("startup")).toBe(true);
    expect(mem.epoch()).toBe(before + 1);
  });
});
