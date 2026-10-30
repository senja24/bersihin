import { describe, expect, test } from "bun:test";
import { CONFIRM_DELAY_MS, canConfirm, needsAsk, pressClean, sourceOfClick, type PressSource } from "./armedClean";

describe("pressClean", () => {
  test("the first press arms and does not clean", () => {
    expect(pressClean(null, 1000, true, "click")).toEqual({ armedAt: 1000, run: false });
  });

  test("a double click right after arming does not clean", () => {
    const first = pressClean(null, 1000, true, "click");
    const second = pressClean(first.armedAt, 1080, true, "click");
    expect(second.run).toBe(false);
    expect(second.armedAt).toBe(1000);
  });

  test("a click just under the delay is ignored, at the delay it cleans", () => {
    expect(pressClean(1000, 1000 + CONFIRM_DELAY_MS - 1, true, "click").run).toBe(false);
    expect(pressClean(1000, 1000 + CONFIRM_DELAY_MS, true, "click")).toEqual({ armedAt: null, run: true });
  });

  test("the confirm delay is at least 600 ms", () => {
    expect(CONFIRM_DELAY_MS).toBeGreaterThanOrEqual(600);
  });

  test("a key press never confirms, even long after arming", () => {
    expect(pressClean(1000, 4000, true, "key").run).toBe(false);
  });

  test("a bare Enter does nothing when no confirmation is needed, it does not even arm", () => {
    expect(pressClean(null, 1000, false, "key")).toEqual({ armedAt: null, run: false });
  });

  test("auto-repeated keyboard clicks never confirm, however long the key is held", () => {
    let armedAt: number | null = null;
    let ran = false;
    for (const now of [1000, 1700, 2400, 3100, 3800]) {
      const press = pressClean(armedAt, now, true, sourceOfClick(0));
      armedAt = press.armedAt;
      ran = ran || press.run;
    }
    expect(ran).toBe(false);
  });

  test("a keyboard click with no confirmation needed cleans once", () => {
    expect(pressClean(null, 1000, false, "keyclick").run).toBe(true);
  });

  test("a click cleans at once when no confirmation is needed", () => {
    expect(pressClean(null, 1000, false, "click").run).toBe(true);
  });
});

describe("needsAsk", () => {
  test("permanent categories always ask", () => {
    expect(needsAsk(false, true)).toBe(true);
    expect(needsAsk(undefined, false)).toBe(true);
    expect(needsAsk(true, false)).toBe(true);
    expect(needsAsk(false, false)).toBe(false);
  });
});

describe("sourceOfClick", () => {
  test("detail 0 is a keyboard click, a pointer click has detail 1 or more", () => {
    const sources: PressSource[] = [sourceOfClick(0), sourceOfClick(1), sourceOfClick(2)];
    expect(sources).toEqual(["keyclick", "click", "click"]);
  });
});

describe("canConfirm (confirm bars)", () => {
  test("a double click right after the bar appears does not confirm", () => {
    expect(canConfirm(1000, 1080, "click")).toBe(false);
    expect(canConfirm(1000, 1000, "click")).toBe(false);
  });

  test("just under the delay is ignored, at the delay a click confirms", () => {
    expect(canConfirm(1000, 1000 + CONFIRM_DELAY_MS - 1, "click")).toBe(false);
    expect(canConfirm(1000, 1000 + CONFIRM_DELAY_MS, "click")).toBe(true);
  });

  test("a keyboard click is ignored inside the delay and allowed after it", () => {
    expect(canConfirm(1000, 1100, "keyclick")).toBe(false);
    expect(canConfirm(1000, 1000 + CONFIRM_DELAY_MS, "keyclick")).toBe(true);
  });

  test("the global Enter handler never confirms", () => {
    expect(canConfirm(1000, 9000, "key")).toBe(false);
  });
});
