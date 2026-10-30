import { describe, expect, test } from "bun:test";
import { OCTAVES, PALETTE, SPECS } from "./mist";
import { fragmentSource } from "./mistGl";

// The shader runs only on a GPU, so these tests keep its constants tied to the CPU reference in mist.ts.
// The pixel comparison of both is done in a browser: see the report of the change that added mistGl.ts.
describe("mist shader", () => {
  for (const variant of Object.keys(SPECS) as (keyof typeof SPECS)[]) {
    test(`${variant}: size, scale, octaves and palette come from mist.ts`, () => {
      const src = fragmentSource(variant);
      const spec = SPECS[variant];
      expect(src).toContain(`const int OCTAVES = ${OCTAVES};`);
      expect(src).toContain(`const vec2 SIZE = vec2(${spec.w}.0, ${spec.h}.0);`);
      expect(src).toContain(`const float SCALE = ${spec.scale};`);
      for (const [name, [r, g, b]] of Object.entries(PALETTE)) {
        expect(src).toContain(`const vec3 ${name} = vec3(${r}.0, ${g}.0, ${b}.0) / 255.0;`);
      }
    });
  }

  test("the third cloud is computed only for the variant that uses it", () => {
    expect(fragmentSource("page")).toContain("float c = cloud(");
    expect(fragmentSource("plume")).toContain("float c = 0.0;");
  });
});
