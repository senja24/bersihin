import { describe, expect, test } from "bun:test";
import { mergeOriginals, needsPreview, shortenContext, shortenPath } from "./aiPreview";

const HOME = "/Users/ana";

describe("shortenPath", () => {
  test("home becomes ~", () => {
    expect(shortenPath("/Users/ana", HOME)).toBe("~");
    expect(shortenPath("/Users/ana/Movies/a.mov", HOME)).toBe("~/Movies/a.mov");
  });

  test("another user's folder loses the user name", () => {
    expect(shortenPath("/Users/anabel/x", HOME)).toBe("/Users/…/x");
    expect(shortenPath("/Users/Shared/x", HOME)).toBe("/Users/…/x");
    expect(shortenPath("/Users/anabel", HOME)).toBe("/Users/…");
  });

  test("the Data volume firmlink form is the same path", () => {
    expect(shortenPath("/System/Volumes/Data/Users/ana/Movies/a.mov", HOME)).toBe("~/Movies/a.mov");
    expect(shortenPath("/System/Volumes/Data/Users/bob/Movies/a.mov", HOME)).toBe("/Users/…/Movies/a.mov");
    expect(shortenPath("/System/Volumes/Data/opt/x", HOME)).toBe("/opt/x");
  });

  test("a user folder on another volume loses the user name too", () => {
    expect(shortenPath("/Volumes/Macintosh HD/Users/ana/x", HOME)).toBe("Macintosh HD/Users/…/x");
  });

  test("a mounted volume keeps only its name and the relative path", () => {
    expect(shortenPath("/Volumes/Studio/Footage/a.mov", HOME)).toBe("Studio/Footage/a.mov");
    expect(shortenPath("/Volumes/Studio", HOME)).toBe("Studio");
    expect(shortenPath("/Volumes/Studio/", HOME)).toBe("Studio");
  });

  test("other paths are left alone", () => {
    expect(shortenPath("/opt/data/x", HOME)).toBe("/opt/data/x");
    expect(shortenPath("/Volumes", HOME)).toBe("/Volumes");
  });

  test("an unknown home still never lets a user name through", () => {
    expect(shortenPath("/Users/ana/x", "")).toBe("/Users/…/x");
    expect(shortenPath("/System/Volumes/Data/Users/ana/x", "")).toBe("/Users/…/x");
  });
});

describe("shortenContext", () => {
  test("shortens large file and junk paths and remembers the originals", () => {
    const { context, originals } = shortenContext(
      {
        junk: [{ name: "Caches", size: 5, locations: ["/Users/ana/Library/Caches", "/Volumes/Studio/.Trashes"] }],
        largeFiles: [{ path: "/Volumes/Studio/Footage/a.mov", size: 9, modified: 1 }],
      },
      HOME,
    );
    expect(context.junk?.[0]?.locations).toEqual(["~/Library/Caches", "Studio/.Trashes"]);
    expect(context.largeFiles?.[0]?.path).toBe("Studio/Footage/a.mov");
    expect(originals.get("Studio/Footage/a.mov")).toBe("/Volumes/Studio/Footage/a.mov");
  });
});

describe("needsPreview", () => {
  test("the first send needs a confirmation, later ones to the same vendor do not", () => {
    expect(needsPreview(null, "claude")).toBe(true);
    expect(needsPreview("claude", "claude")).toBe(false);
  });

  test("switching vendor needs a new confirmation", () => {
    expect(needsPreview("claude", "chatgpt")).toBe(true);
  });
});

describe("originals", () => {
  test("junk locations on a volume map back, so they can be revealed", () => {
    const { originals } = shortenContext({ junk: [{ name: "Trash", size: 1, locations: ["/Volumes/Studio/.Trashes"] }] }, HOME);
    expect(originals.get("Studio/.Trashes")).toBe("/Volumes/Studio/.Trashes");
  });

  test("two real paths with the same short form are ambiguous and map to nothing", () => {
    const { originals } = shortenContext(
      { largeFiles: [{ path: "/Users/bob/a.zip", size: 1, modified: 1 }, { path: "/Users/eve/a.zip", size: 1, modified: 1 }] },
      HOME,
    );
    expect(originals.get("/Users/…/a.zip")).toBeNull();
  });

  test("a collision across two builds is ambiguous as well", () => {
    const all = new Map<string, string | null>();
    mergeOriginals(all, new Map([["/Users/…/a.zip", "/Users/bob/a.zip"]]));
    mergeOriginals(all, new Map([["/Users/…/a.zip", "/Users/eve/a.zip"]]));
    expect(all.get("/Users/…/a.zip")).toBeNull();
    mergeOriginals(all, new Map([["/Users/…/a.zip", "/Users/bob/a.zip"]]));
    expect(all.get("/Users/…/a.zip")).toBeNull();
  });

  test("the same real path twice is not a collision", () => {
    const all = new Map<string, string | null>([["Studio/x", "/Volumes/Studio/x"]]);
    mergeOriginals(all, new Map([["Studio/x", "/Volumes/Studio/x"]]));
    expect(all.get("Studio/x")).toBe("/Volumes/Studio/x");
  });
});
