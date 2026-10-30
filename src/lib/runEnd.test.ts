import { describe, expect, test } from "bun:test";
import { onRunEnd, runEnded } from "./runEnd";

describe("runEnd", () => {
  test("listeners hear which run ended until they unsubscribe", () => {
    const seen: string[] = [];
    const off = onRunEnd((id) => void seen.push(id));
    runEnded("a");
    off();
    runEnded("b");
    expect(seen).toEqual(["a"]);
  });
});
