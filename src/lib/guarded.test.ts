import { describe, expect, test } from "bun:test";
import { createGuarded, rejectAsError, type GoBackend } from "./guarded";
import { RUN_ID_PATTERN } from "./runId";
import type { CleanResult } from "../types";

const result: CleanResult = { freed: 0, removed: 0, failed: 0, denied: 0, skipped: [], notes: [], cancelled: false };

function fake(over: { mintFails?: boolean } = {}) {
  const log: string[] = [];
  const cancelled: string[] = [];
  const mint = (name: string) => async (...a: unknown[]) => {
    log.push(`confirm.${name}(${a.join("|")})`);
    if (over.mintFails) throw new Error("Invalid request");
    return `token-${name}`;
  };
  const hang: Record<string, () => void> = {};
  const go: GoBackend = {
    confirm: { clean: mint("clean"), trash: mint("trash"), uninstall: mint("uninstall"), startup: mint("startup") },
    junk: {
      scan: async (id) => (log.push(`junk.scan(${id})`), []),
      clean: async (run, scan, ids, token) => (log.push(`junk.clean(${RUN_ID_PATTERN.test(run)}|${scan}|${ids.join(",")}|${token})`), result),
    },
    files: {
      scanSpaces: (id, root) => new Promise((res) => { log.push(`spaces(${id}|${root})`); hang[id] = () => res({} as never); }),
      scanLargeFiles: async () => ({ files: [], hidden: 0 }),
      scanDuplicates: async () => ({ groups: [], hidden: 0 }),
      trashPaths: async (run, scan, paths, token) => (log.push(`files.trashPaths(${RUN_ID_PATTERN.test(run)}|${scan}|${paths.join(",")}|${token})`), result),
    },
    apps: {
      list: async () => [],
      uninstall: async (scan, id, token) => (log.push(`apps.uninstall(${scan}|${id}|${token})`), { ok: true, message: "" }),
    },
    startup: { set: async (id, on, token) => (log.push(`startup.set(${id}|${on}|${token})`), []) },
    system: { cancelScan: async (id) => (cancelled.push(id), true) },
  };
  let n = 0;
  return { api: createGuarded(go, () => `generated-run-id-${n++}`), log, cancelled, hang };
}

describe("destructive calls mint a token first (S2)", () => {
  test("cleanJunk", async () => {
    const f = fake();
    await f.api.cleanJunk("scan-1", ["a", "b"]);
    expect(f.log).toEqual(["confirm.clean(scan-1|a,b)", "junk.clean(true|scan-1|a,b|token-clean)"]);
  });

  test("trashPaths, uninstallApp and setStartupItem", async () => {
    const f = fake();
    await f.api.trashPaths("scan-2", ["/p"]);
    await f.api.uninstallApp("scan-3", "app");
    await f.api.setStartupItem("login", true);
    expect(f.log).toEqual([
      "confirm.trash(scan-2|/p)", "files.trashPaths(true|scan-2|/p|token-trash)",
      "confirm.uninstall(scan-3|app)", "apps.uninstall(scan-3|app|token-uninstall)",
      "confirm.startup(login|true)", "startup.set(login|true|token-startup)",
    ]);
  });

  test("when Go refuses the token nothing destructive is called", async () => {
    const f = fake({ mintFails: true });
    await expect(f.api.cleanJunk("s", ["a"])).rejects.toThrow("Invalid request");
    await expect(f.api.trashPaths("s", ["/p"])).rejects.toThrow("Invalid request");
    await expect(f.api.uninstallApp("s", "a")).rejects.toThrow("Invalid request");
    await expect(f.api.setStartupItem("a", false)).rejects.toThrow("Invalid request");
    expect(f.log.every((l) => l.startsWith("confirm."))).toBe(true);
  });

  test("every call mints its own token", async () => {
    const f = fake();
    await f.api.cleanJunk("s", ["a"]);
    await f.api.cleanJunk("s", ["a"]);
    expect(f.log.filter((l) => l.startsWith("confirm."))).toHaveLength(2);
  });
});

describe("cancelScan goes by run id (M6)", () => {
  test("cancelling spaces stops both roots by their own ids and leaves other tasks alone", async () => {
    const f = fake();
    void f.api.scanSpaces("run-home-0000000000", undefined);
    void f.api.scanSpaces("run-drive-000000000", "/Volumes/X");
    void f.api.scanJunk("run-junk-0000000000");
    await f.api.cancelScan(["spaces"]);
    expect(f.cancelled.sort()).toEqual(["run-drive-000000000", "run-home-0000000000"]);
  });

  test("no argument cancels everything running; finished runs are not cancelled", async () => {
    const f = fake();
    void f.api.scanSpaces("run-a-00000000000000", "/a");
    await f.api.scanJunk("run-junk-0000000000");
    await f.api.cancelScan();
    expect(f.cancelled).toEqual(["run-a-00000000000000"]);
  });

  test("a trash run is cancellable by its task", async () => {
    const cancelled: string[] = [];
    let release: () => void = () => undefined;
    const pending = new Promise<void>((r) => (release = r));
    const api = createGuarded({
      ...({} as GoBackend),
      confirm: { clean: async () => "t", trash: async () => "t", uninstall: async () => "t", startup: async () => "t" },
      files: { ...({} as GoBackend["files"]), trashPaths: async () => (await pending, result) },
      system: { cancelScan: async (id) => (cancelled.push(id), true) },
    }, () => "trash-run-id-00000000");
    const run = api.trashPaths("s", ["/p"]);
    await new Promise((r) => setTimeout(r, 0));
    await api.cancelScan(["trash"]);
    release();
    await run;
    expect(cancelled).toEqual(["trash-run-id-00000000"]);
  });

  test("Stop pressed while the token is being minted is not lost: nothing destructive is sent (M4)", async () => {
    const givers: ((t: string) => void)[] = [];
    const log: string[] = [];
    const api = createGuarded({
      ...({} as GoBackend),
      confirm: { clean: () => new Promise((r) => void givers.push(r)), trash: () => new Promise((r) => void givers.push(r)), uninstall: async () => "t", startup: async () => "t" },
      junk: { scan: async () => [], clean: async () => (log.push("clean"), result) },
      files: { ...({} as GoBackend["files"]), trashPaths: async () => (log.push("trash"), result) },
      system: { cancelScan: async () => (log.push("cancel"), true) },
    }, (() => { let n = 0; return () => `pending-run-id-00000${n++}`; })());
    const trash = api.trashPaths("s", ["/p"]);
    const clean = api.cleanJunk("s", ["a"]);
    await api.cancelScan(["trash", "clean"]);
    givers.forEach((give) => give("tok"));
    expect((await trash).cancelled).toBe(true);
    expect((await clean).cancelled).toBe(true);
    expect(log).toEqual([]);
  });

  test("a run is no longer cancellable once it has finished", async () => {
    const f = fake();
    await f.api.cleanJunk("s", ["a"]);
    await f.api.cancelScan();
    expect(f.cancelled).toEqual([]);
  });
});

describe("runs ending (M6: a finished run must not leave its last progress on screen)", () => {
  test("every tracked run reports its end, on success and on failure", async () => {
    const ended: string[] = [];
    const go = fake().api;
    void go;
    const failing = createGuarded({
      ...({} as GoBackend),
      junk: { scan: async () => { throw new Error("boom"); }, clean: async () => result },
      files: { ...({} as GoBackend["files"]), scanLargeFiles: async () => ({ files: [], hidden: 0 }), trashPaths: async () => result },
      confirm: { clean: async () => "t", trash: async () => "t", uninstall: async () => "t", startup: async () => "t" },
      system: { cancelScan: async () => true },
    }, () => "minted-run-id-00000000", (id) => void ended.push(id));
    await failing.scanJunk("scan-run-id-0000000").catch(() => undefined);
    await failing.scanLargeFiles("large-run-id-000000", 1);
    await failing.trashPaths("s", ["/p"]);
    expect(ended).toEqual(["scan-run-id-0000000", "large-run-id-000000", "minted-run-id-00000000"]);
  });
});

describe("rejectAsError", () => {
  test("keeps a CallError as it is and wraps a string", async () => {
    class CallError extends Error {}
    const e = new CallError("Invalid request");
    await expect(rejectAsError(Promise.reject(e))).rejects.toBe(e);
    await expect(rejectAsError(Promise.reject("plain"))).rejects.toThrow("plain");
  });
});

