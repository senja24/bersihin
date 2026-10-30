import { describe, expect, test } from "bun:test";
import { countOf, hiddenNote, isIrreversible } from "./format";
import type { JunkCategory } from "../types";

const cat = (over: Partial<JunkCategory>): JunkCategory => ({
  id: "x", name: "X", description: "", icon: "broom", size: 1, count: 1, locations: [], safe: true, irreversible: false, ...over,
});

describe("isIrreversible follows the flag from Go, not the name", () => {
  test("a cache is irreversible even though its name says nothing about the Trash", () => {
    expect(isIrreversible(cat({ id: "mac-caches", name: "User caches", irreversible: true }))).toBe(true);
  });

  test("a category named Trash that Go says is reversible is not irreversible", () => {
    expect(isIrreversible(cat({ id: "trash-like", name: "Trash", irreversible: false }))).toBe(false);
  });

  test("old downloads (moved to the Trash) are reversible", () => {
    expect(isIrreversible(cat({ id: "old-downloads", name: "Old downloads", irreversible: false }))).toBe(false);
  });
});

describe("hiddenNote", () => {
  test("says nothing when nothing is hidden", () => {
    expect(hiddenNote(0, "file", "files")).toBeNull();
  });
  test("uses the singular for one and the plural otherwise", () => {
    expect(hiddenNote(1, "copy", "copies")).toBe("1 copy hidden by safety rules");
    expect(hiddenNote(12, "file", "files")).toBe("12 files hidden by safety rules");
  });
});

describe("countOf", () => {
  test("pairs the number with the right word", () => {
    expect(countOf(0, "file", "files")).toBe("0 files");
    expect(countOf(1, "file", "files")).toBe("1 file");
    expect(countOf(2, "copy", "copies")).toBe("2 copies");
  });
});
