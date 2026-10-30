import { describe, expect, test } from "bun:test";
import { changeStartup } from "./startupChange";
import type { StartupItem } from "../types";

const item = (enabled: boolean): StartupItem => ({ id: "x", name: "Slack", command: "", location: "Login Items", enabled, impact: "low", canToggle: true, needsAdmin: false });

describe("changeStartup (M7: show the true saved state after a rejection)", () => {
  test("success shows the list Go returned and does not reload", async () => {
    const log: string[] = [];
    const list = [item(false)];
    await changeStartup({
      set: async () => list,
      show: (l) => void log.push(`show:${l.length}`),
      reload: async () => void log.push("reload"),
      notify: (t, tone) => void log.push(`${tone}:${t}`),
    }, item(true), false);
    expect(log).toEqual(["show:1", "ok:Turned off Slack - applies from the next start"]);
  });

  test("a rejection (saved, but macOS could not apply it) reloads the list and reports the message", async () => {
    const log: string[] = [];
    await changeStartup({
      set: async () => { throw new Error("saved, but macOS could not apply it now"); },
      show: () => void log.push("show"),
      reload: async () => void log.push("reload"),
      notify: (t, tone) => void log.push(`${tone}:${t}`),
    }, item(false), true);
    expect(log).toEqual(["err:saved, but macOS could not apply it now", "reload"]);
  });

  test("a failing reload does not hide the first error", async () => {
    const log: string[] = [];
    await changeStartup({
      set: async () => { throw new Error("nope"); },
      show: () => undefined,
      reload: async () => { throw new Error("reload broke"); },
      notify: (t) => void log.push(t),
    }, item(false), true);
    expect(log).toEqual(["nope"]);
  });
});
