import { describe, expect, test } from "bun:test";
import { applyProgress, dropRun, dropTask, pick } from "./live";
import type { ScanProgress } from "../types";

const p = (id: string, task: ScanProgress["task"], phase = "x"): ScanProgress => ({ id, task, phase, done: 1, total: 2 });

describe("live progress slots (M6)", () => {
  test("two concurrent runs of the same task keep separate slots", () => {
    let s = applyProgress({}, p("a", "spaces", "Home"));
    s = applyProgress(s, p("b", "spaces", "Drive"));
    expect(Object.keys(s)).toEqual(["a", "b"]);
    expect(s["a"]!.phase).toBe("Home");
  });

  test("dropRun removes only that run", () => {
    let s = applyProgress({}, p("a", "spaces"));
    s = applyProgress(s, p("b", "spaces"));
    expect(Object.keys(dropRun(s, "a"))).toEqual(["b"]);
    expect(dropRun(s, "zzz")).toBe(s);
  });

  test("dropTask removes every run of the task", () => {
    let s = applyProgress({}, p("a", "clean"));
    s = applyProgress(s, p("b", "junk"));
    expect(Object.keys(dropTask(s, "clean"))).toEqual(["b"]);
  });

  test("pick returns the newest slot among the wanted tasks, any when none are named", () => {
    let s = applyProgress({}, p("a", "junk", "first"));
    s = applyProgress(s, p("b", "large", "second"));
    s = applyProgress(s, p("c", "junk", "third"));
    expect(pick(s, ["junk"])?.phase).toBe("third");
    expect(pick(s, ["large"])?.phase).toBe("second");
    expect(pick(s)?.phase).toBe("third");
    expect(pick(s, ["dupes"])).toBeNull();
  });

  test("an update moves a run to newest", () => {
    let s = applyProgress({}, p("a", "junk", "a1"));
    s = applyProgress(s, p("b", "junk", "b1"));
    s = applyProgress(s, p("a", "junk", "a2"));
    expect(pick(s, ["junk"])?.phase).toBe("a2");
  });

  test("the cap of 16 holds and evicts the oldest", () => {
    let s = {};
    for (let i = 0; i < 20; i++) s = applyProgress(s, p(`run-${i}`, "large"));
    expect(Object.keys(s)).toHaveLength(16);
    expect(s).not.toHaveProperty("run-0");
    expect(s).toHaveProperty("run-19");
  });

  test("does not mutate its input", () => {
    const s = applyProgress({}, p("a", "junk"));
    applyProgress(s, p("b", "junk"));
    expect(Object.keys(s)).toEqual(["a"]);
  });
});
