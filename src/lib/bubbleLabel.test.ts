import { describe, expect, test } from "bun:test";
import { GLYPH_EM, LABEL_FLOOR, LABEL_WIDTH_SHARE, labelSize } from "./bubbleLabel";

const fits = (name: string, radius: number, size: number) => name.length * size * GLYPH_EM <= 2 * radius * LABEL_WIDTH_SHARE;

describe("labelSize", () => {
  test("a short name in the smallest bubble shrinks until it fits", () => {
    const size = labelSize("Desktop", 24, 11.5);
    expect(size).toBeLessThan(11.5);
    expect(fits("Desktop", 24, size)).toBe(true);
  });

  test("a name that already fits keeps the base size", () => {
    expect(labelSize("Music", 60, 12.5)).toBe(12.5);
  });

  test("a long name never shrinks below the floor", () => {
    expect(labelSize("Application Support Files", 24, 11.5)).toBe(LABEL_FLOOR);
  });

  test("an empty name keeps the base size", () => {
    expect(labelSize("", 24, 11.5)).toBe(11.5);
  });
});
