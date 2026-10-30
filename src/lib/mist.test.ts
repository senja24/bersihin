import { describe, expect, test } from "bun:test";
import { SPECS, cloud, fillMist, samplesPerSecond } from "./mist";

// Smart Scan paints both canvases at once; before the fix this was about 4.96 million lookups per second.
const BUDGET_PER_SECOND = 2_000_000;

describe("mist cost", () => {
  test("both canvases together stay within the noise-lookup budget", () => {
    const total = samplesPerSecond(SPECS.page) + samplesPerSecond(SPECS.plume);
    expect(total).toBeLessThanOrEqual(BUDGET_PER_SECOND);
  });
});

describe("mist look", () => {
  test("the cloud field stays in 0..1 and centred, whatever the octave count", () => {
    let sum = 0;
    let n = 0;
    for (let x = 0; x < 40; x += 0.7) {
      for (let y = 0; y < 40; y += 0.7) {
        const v = cloud(x, y);
        expect(v).toBeGreaterThanOrEqual(0);
        expect(v).toBeLessThanOrEqual(1);
        sum += v;
        n++;
      }
    }
    expect(sum / n).toBeGreaterThan(0.4);
    expect(sum / n).toBeLessThan(0.6);
  });

  test("the page layer paints every pixel opaque and the plume stays translucent", () => {
    const page = new Uint8ClampedArray(SPECS.page.w * SPECS.page.h * 4);
    fillMist(page, SPECS.page, 4);
    expect(page.filter((_, i) => i % 4 === 3).every((a) => a === 255)).toBe(true);
    const plume = new Uint8ClampedArray(SPECS.plume.w * SPECS.plume.h * 4);
    fillMist(plume, SPECS.plume, 9);
    expect(plume.some((v, i) => i % 4 === 3 && v > 0 && v < 255)).toBe(true);
  });
});
