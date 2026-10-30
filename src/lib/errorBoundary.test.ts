import { describe, expect, test } from "bun:test";
import { describeError, initialBoundary, nextBoundary } from "./errorBoundary";

describe("nextBoundary", () => {
  test("a render error switches to the fallback", () => {
    const next = nextBoundary(initialBoundary, { type: "error", error: new Error("boom") });
    expect(next.error?.message).toBe("boom");
  });

  test("a thrown non-Error value is wrapped", () => {
    const next = nextBoundary(initialBoundary, { type: "error", error: "oops" });
    expect(next.error).toBeInstanceOf(Error);
    expect(next.error?.message).toBe("oops");
  });

  test("reset clears the error", () => {
    const failed = nextBoundary(initialBoundary, { type: "error", error: new Error("boom") });
    expect(nextBoundary(failed, { type: "reset" })).toEqual(initialBoundary);
  });

  test("a later error replaces the first one", () => {
    const first = nextBoundary(initialBoundary, { type: "error", error: new Error("a") });
    expect(nextBoundary(first, { type: "error", error: new Error("b") }).error?.message).toBe("b");
  });
});

describe("describeError", () => {
  test("falls back to a plain sentence for an empty message", () => {
    expect(describeError(new Error(""))).toBe("Something went wrong while drawing this screen.");
    expect(describeError(new Error("boom"))).toBe("boom");
  });
});
