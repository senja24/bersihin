import { describe, expect, test } from "bun:test";
import { createSingleFlight } from "./singleFlight";

describe("createSingleFlight (S2: a double click must not mint two tokens)", () => {
  test("a second call while the first runs does nothing", async () => {
    const guard = createSingleFlight();
    let calls = 0;
    let release: () => void = () => undefined;
    const gate = new Promise<void>((r) => (release = r));
    const task = async () => {
      calls += 1;
      await gate;
    };
    const first = guard(task);
    const second = guard(task);
    expect(calls).toBe(1);
    release();
    await first;
    await second;
    expect(calls).toBe(1);
  });

  test("it can run again after finishing, also after a failure", async () => {
    const guard = createSingleFlight();
    let calls = 0;
    await guard(async () => void (calls += 1));
    await guard(async () => { calls += 1; throw new Error("x"); }).catch(() => undefined);
    await guard(async () => void (calls += 1));
    expect(calls).toBe(3);
  });
});
