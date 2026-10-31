import { describe, expect, test } from "bun:test";
import { MAC, platformOf } from "./platform";
import { appWords, iconSourceOf, protectionNote, startupWords } from "./systemWords";
import type { StartupItem } from "../types";

const WIN = platformOf({ platform: "windows", trashName: "Recycle Bin", fileManager: "File Explorer", unsupported: [] });
const item = (location: string): StartupItem => ({ id: "x", name: "X", command: "", location, enabled: true, impact: "low", canToggle: false, needsAdmin: true });

describe("system words", () => {
  test("Applications says what happens on each platform", () => {
    expect(appWords(MAC).sub).toContain("Mac");
    expect(appWords(WIN).sub).toContain("uninstaller");
    expect(appWords(WIN).lockedTitle).toContain("Settings > Apps");
    expect(appWords(MAC).lockedTitle).toContain("macOS");
  });
  test("Speed never mentions macOS or a password on Windows", () => {
    const w = startupWords(WIN);
    expect(`${w.password}${w.passwordNote}${w.looking}${w.empty}`).not.toMatch(/mac|password/i);
    expect(w.locked(item("Run key (all users)")).title).toContain("Task Manager");
    expect(w.locked(item("Task Scheduler")).label).toBe("Task Scheduler");
    expect(startupWords(MAC).password).toContain("macOS");
  });
  test("Protection says it only reads on Windows", () => {
    expect(protectionNote(WIN)).toContain("changes nothing");
    expect(protectionNote(MAC)).toContain("Mac");
  });
  test("icons come from the app bundle or the program", () => {
    expect(iconSourceOf("/Applications/Spotify.app/Contents/MacOS/Spotify --x")).toBe("/Applications/Spotify.app");
    expect(iconSourceOf('"C:\\Program Files\\Steam\\steam.exe" -silent')).toBe("C:\\Program Files\\Steam\\steam.exe");
    expect(iconSourceOf("C:\\Tools\\clip.exe -q")).toBe("C:\\Tools\\clip.exe");
    expect(iconSourceOf("%windir%\\system32\\x.exe")).toBeUndefined();
    expect(iconSourceOf("")).toBeUndefined();
  });
  test("the platform names its OS", () => {
    expect(MAC.os).toBe("macOS");
    expect(WIN.os).toBe("Windows");
  });
});
