import { describe, expect, test } from "bun:test";
import { MAC, isWinPath, leafOf, parentOf, platformOf, sepOf, within } from "./platform";

const WIN = { platform: "windows" as const, trashName: "Recycle Bin", fileManager: "File Explorer", unsupported: ["applications", "startup", "protection"] };

describe("platformOf", () => {
  test("the Mac until Go has said otherwise", () => {
    expect(platformOf(null)).toBe(MAC);
    expect(platformOf({ platform: "darwin", trashName: "Trash", fileManager: "Finder", unsupported: [] })).toMatchObject({ windows: false, trash: "Trash", computer: "Mac" });
  });
  test("Windows words and hidden screens come from Go", () => {
    const p = platformOf(WIN);
    expect(p).toMatchObject({ windows: true, trash: "Recycle Bin", fileManager: "File Explorer", computer: "PC" });
    expect([...p.unsupported].sort()).toEqual(["applications", "protection", "startup"]);
  });
  test("a backend that sent no words still gets the right ones", () => {
    expect(platformOf({ platform: "windows", trashName: "", fileManager: "", unsupported: [] })).toMatchObject({ trash: "Recycle Bin", fileManager: "File Explorer" });
  });
});

describe("Windows paths", () => {
  test("are told from Mac paths", () => {
    expect(isWinPath("C:\\Users\\ika")).toBe(true);
    expect(isWinPath("d:/Games")).toBe(true);
    expect(isWinPath("~\\Downloads")).toBe(true);
    expect(isWinPath("/Users/ika")).toBe(false);
    expect(isWinPath("~/Downloads")).toBe(false);
    expect(sepOf("C:\\x")).toBe("\\");
    expect(sepOf("/x")).toBe("/");
  });
  test("leaf and parent", () => {
    expect(leafOf("C:\\Users\\ika\\Videos\\a.mkv")).toBe("a.mkv");
    expect(leafOf("D:\\")).toBe("D:");
    expect(parentOf("C:\\Users\\ika\\Videos\\a.mkv")).toBe("C:\\Users\\ika\\Videos");
    expect(parentOf("D:\\Games")).toBe("D:\\");
    expect(parentOf("~\\Downloads\\a.zip")).toBe("~\\Downloads");
  });
  test("a backslash stays part of a Mac name", () => {
    expect(leafOf("/Users/ika/a\\b.txt")).toBe("a\\b.txt");
    expect(parentOf("/Users/ika/a\\b.txt")).toBe("/Users/ika");
    expect(parentOf("/Applications")).toBe("/");
  });
  test("within compares Windows paths without case", () => {
    expect(within("C:\\Users\\ika", "c:\\users\\IKA\\Downloads")).toBe(true);
    expect(within("C:\\Users\\ika", "C:\\Users\\ika")).toBe(true);
    expect(within("C:\\Users\\ika", "C:\\Users\\ikaros\\x")).toBe(false);
    expect(within("D:\\", "D:\\Games\\x")).toBe(true);
    expect(within("/Users/ika", "/Users/ika/x")).toBe(true);
    expect(within("/Users/ika", "/users/ika/x")).toBe(false);
    expect(within("/", "/Applications")).toBe(true);
  });
});

describe("Windows paths elsewhere on the page", () => {
  test("tildePath writes the profile as ~ in any case", async () => {
    const { tildePath, leafName, dirOf } = await import("./format");
    expect(tildePath("C:\\Users\\ika\\Downloads\\a.zip", "C:\\Users\\ika")).toBe("~\\Downloads\\a.zip");
    expect(tildePath("c:\\users\\IKA", "C:\\Users\\ika")).toBe("~");
    expect(tildePath("C:\\Users\\ikaros\\a", "C:\\Users\\ika")).toBe("C:\\Users\\ikaros\\a");
    expect(tildePath("/Users/ika/x", "/Users/ika")).toBe("~/x");
    expect(leafName("D:\\Games\\old.iso")).toBe("old.iso");
    expect(dirOf("D:\\Games\\old.iso")).toBe("D:\\Games");
  });
  test("shortenPath hides every user name of a Windows path", async () => {
    const { shortenPath } = await import("./aiPreview");
    const home = "C:\\Users\\ika";
    expect(shortenPath("C:\\Users\\ika\\Videos\\a.mkv", home)).toBe("~\\Videos\\a.mkv");
    expect(shortenPath("c:/users/IKA/Videos", home)).toBe("~\\Videos");
    expect(shortenPath("C:\\Users\\bob\\Desktop\\x", home)).toBe("C:\\Users\\…\\Desktop\\x");
    expect(shortenPath("D:\\Users\\old-pc\\x", home)).toBe("D:\\Users\\…\\x");
    expect(shortenPath("D:\\Games\\x.iso", home)).toBe("D:\\Games\\x.iso");
  });
  test("volumeOf names the drive and the folder on it", async () => {
    const { volumeOf } = await import("../store");
    const vols = [
      { mount: "C:\\", label: "Local Disk (C:)", total: 1, free: 1, used: 0, system: true },
      { mount: "D:\\", label: "Data (D:)", total: 1, free: 1, used: 0, system: false },
    ];
    expect(volumeOf("C:\\Users\\ika\\a.iso", "C:\\Users\\ika", vols)).toBeNull();
    expect(volumeOf("D:\\Games\\x\\a.iso", "C:\\Users\\ika", vols)).toEqual({ name: "Data (D:)", dir: "\\Games\\x" });
    expect(volumeOf("D:\\a.iso", "C:\\Users\\ika", vols)).toEqual({ name: "Data (D:)", dir: "\\" });
  });
});
