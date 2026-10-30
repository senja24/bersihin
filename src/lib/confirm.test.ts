import { describe, expect, test } from "bun:test";
import { isConfirmError, isScanExpired } from "./confirm";
import { wasCancelled } from "./format";

// What the page gets: MyGo rejects with a CallError whose message is err.Error() of the Go error
// (reference/mygo ipc.go encodeReply). A Go method that wraps with %w would add a prefix.
class CallError extends Error {
  constructor(readonly method: string, message: string) {
    super(message);
    this.name = "CallError";
  }
}

describe("the real MyGo error shape", () => {
  test("a bare CallError is matched", () => {
    expect(isConfirmError(new CallError("Junk.Clean", "Confirmation expired"))).toBe(true);
    expect(isScanExpired(new CallError("Files.TrashPaths", "Scan expired: scan again"))).toBe(true);
  });

  test("a wrapped message is matched too", () => {
    expect(isConfirmError(new CallError("Junk.Clean", "clean: Confirmation required"))).toBe(true);
    expect(isScanExpired(new CallError("Apps.Uninstall", "uninstall: Scan expired: scan again"))).toBe(true);
  });

  test("unrelated messages are not matched", () => {
    expect(isConfirmError(new CallError("x", "Invalid request"))).toBe(false);
    expect(isScanExpired(new CallError("x", "Confirmation expired"))).toBe(false);
    expect(isConfirmError(new CallError("x", "scan cancelled"))).toBe(false);
  });
});

describe("confirmation errors from Go", () => {
  test("recognises the exact confirmation messages", () => {
    expect(isConfirmError(new Error("Confirmation required"))).toBe(true);
    expect(isConfirmError(new Error("Confirmation expired"))).toBe(true);
    expect(isScanExpired(new Error("Confirmation expired"))).toBe(false);
  });

  test("recognises an expired scan", () => {
    expect(isScanExpired(new Error("Scan expired: scan again"))).toBe(true);
    expect(isConfirmError(new Error("Scan expired: scan again"))).toBe(false);
  });

  test("a cancel message is neither, and no Go message reads as a cancel", () => {
    const cancel = new Error("scan cancelled");
    expect(isConfirmError(cancel)).toBe(false);
    expect(isScanExpired(cancel)).toBe(false);
    expect(wasCancelled(cancel)).toBe(true);
    for (const m of ["Confirmation required", "Confirmation expired", "Scan expired: scan again", "Invalid request", "Invalid request: run id in use"]) {
      expect(wasCancelled(new Error(m))).toBe(false);
    }
  });

  test("non-errors are never matched", () => {
    expect(isConfirmError("Confirmation required")).toBe(false);
    expect(isScanExpired(null)).toBe(false);
  });
});
