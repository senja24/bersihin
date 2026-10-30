import { describe, expect, test } from "bun:test";
import { createStaleGuard } from "./staleGuard";

/** A slow test that reports into `shown` the way useConnectionTest does. */
function slowRun(guard: ReturnType<typeof createStaleGuard>, shown: string[], label: string) {
  let finish: () => void = () => undefined;
  const ticket = guard.start();
  const done = new Promise<void>((r) => (finish = r)).then(() => {
    if (guard.isCurrent(ticket)) shown.push(label);
  });
  return { finish, done };
}

describe("createStaleGuard", () => {
  test("a result that arrives with nothing changed is shown", async () => {
    const guard = createStaleGuard();
    const shown: string[] = [];
    const run = slowRun(guard, shown, "connected");
    run.finish();
    await run.done;
    expect(shown).toEqual(["connected"]);
  });

  test("a result that arrives after the settings changed is dropped", async () => {
    const guard = createStaleGuard();
    const shown: string[] = [];
    const run = slowRun(guard, shown, "connected");
    guard.cancel(); // the address, model or key changed while the test ran
    run.finish();
    await run.done;
    expect(shown).toEqual([]);
  });

  test("a new run after a change is shown, the old one still is not", async () => {
    const guard = createStaleGuard();
    const shown: string[] = [];
    const old = slowRun(guard, shown, "old");
    guard.cancel();
    const fresh = slowRun(guard, shown, "fresh");
    old.finish();
    fresh.finish();
    await Promise.all([old.done, fresh.done]);
    expect(shown).toEqual(["fresh"]);
  });

  test("every change counts, not only the first", async () => {
    const guard = createStaleGuard();
    const shown: string[] = [];
    const run = slowRun(guard, shown, "x");
    guard.cancel();
    guard.cancel();
    run.finish();
    await run.done;
    expect(shown).toEqual([]);
  });
});
