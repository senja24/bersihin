import { describe, expect, test } from "bun:test";
import { RUN_ID_PATTERN, newRunId } from "./runId";

// The same pattern the Go side enforces (internal/progress).
const GO_RUN_ID = /^[A-Za-z0-9_-]{16,64}$/;

describe("newRunId", () => {
  test("matches the pattern Go validates", () => {
    for (let i = 0; i < 200; i++) expect(GO_RUN_ID.test(newRunId())).toBe(true);
  });

  test("exports the same pattern Go uses", () => {
    expect(RUN_ID_PATTERN.source).toBe(GO_RUN_ID.source);
  });

  test("1000 ids are unique", () => {
    expect(new Set(Array.from({ length: 1000 }, newRunId)).size).toBe(1000);
  });
});
