import type { StartupItem } from "../types";
import { messageOf } from "./format";

export interface StartupChangeDeps {
  set: (id: string, enabled: boolean) => Promise<StartupItem[]>;
  show: (items: StartupItem[]) => void;
  reload: () => Promise<void>;
  notify: (text: string, tone: "ok" | "err") => void;
}

/**
 * Turns a login item on or off. Go can save the change and still fail to apply it ("saved, but macOS could not apply
 * it now"), so after any rejection the list is read again and the toggle shows what is really saved.
 */
export async function changeStartup(d: StartupChangeDeps, item: StartupItem, on: boolean): Promise<void> {
  try {
    d.show(await d.set(item.id, on));
    d.notify(`${on ? "Turned on" : "Turned off"} ${item.name} - applies from the next start`, "ok");
  } catch (err) {
    d.notify(messageOf(err, `Could not change ${item.name}`), "err");
    try {
      await d.reload();
    } catch {
      // the first error is the one worth showing
    }
  }
}
