// The words of Applications, Speed and Protection, which do different things on each platform: a Mac app goes to the
// Trash and an admin change asks for the password; a Windows program is removed by its own uninstaller, and startup
// apps of every user are left to Task Manager (Bersihin never asks for administrator rights there).
import type { StartupItem } from "../types";
import type { Platform } from "./platform";

export interface AppWords {
  sub: string;
  ask: string;
  working: string;
  locked: string;
  lockedTitle: string;
}

export function appWords(p: Platform): AppWords {
  if (p.windows) {
    return {
      sub: "Everything installed on this PC. Remove the ones you no longer use: Bersihin opens the program's own uninstaller.",
      ask: "Open its uninstaller?",
      working: "Opening",
      locked: "Protected",
      lockedTitle: "Part of Windows or needed by other programs, or it has no uninstaller. Use Settings > Apps if you must remove it",
    };
  }
  return {
    sub: "Everything installed on this Mac. Remove the ones you no longer use through the app's own uninstaller.",
    ask: "Uninstall?",
    working: "Removing",
    locked: "Built in",
    lockedTitle: "This app is part of macOS",
  };
}

export interface StartupWords {
  /** for an item that may ask for an administrator's password when changed (macOS only) */
  password: string;
  passwordNote: string;
  looking: string;
  empty: string;
  /** what a locked row says, and its tooltip */
  locked: (item: StartupItem) => { label: string; title: string };
}

export function startupWords(p: Platform): StartupWords {
  if (p.windows) {
    return {
      password: "",
      passwordNote: "",
      looking: "Looking at what starts with your PC",
      empty: "Your PC already starts as light as it can.",
      locked: (item) =>
        item.location === "Task Scheduler"
          ? { label: "Task Scheduler", title: "A task that runs at sign-in. Bersihin only shows it: change it in Task Scheduler" }
          : { label: "All users", title: "Starts for every user of this PC. Changing it needs administrator rights, so Bersihin only shows it: use Task Manager > Startup apps" },
    };
  }
  return {
    password: "macOS will ask for your password",
    passwordNote: " Entries tagged Admin apply to every user, so macOS asks for permission first.",
    looking: "Looking at what starts with your Mac",
    empty: "Your Mac already starts as light as it can.",
    locked: () => ({ label: "Managed by the system", title: "Controlled by your system or an administrator policy" }),
  };
}

/** The file to take a login item's icon from: the .app bundle on a Mac, the .exe on Windows. */
export function iconSourceOf(command: string): string | undefined {
  const c = command.trim();
  const app = /^(\/.+?\.app)(?=\/|\s|$)/.exec(c)?.[1];
  if (app) return app;
  return /^"?([A-Za-z]:\\[^"]*?\.exe)(?="|\s|$)/i.exec(c)?.[1];
}

/** How the Protection screen describes itself. */
export function protectionNote(p: Platform): string {
  return p.windows
    ? "Bersihin is not a replacement for your security tools. It reads what Windows Security reports, changes nothing, and says plainly when something is off or out of date."
    : "Bersihin is not a replacement for your security tools. It confirms that the protection already built into your Mac is on and current, and says plainly when it is not.";
}
