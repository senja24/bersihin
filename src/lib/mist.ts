// Soft pastel clouds: fractal value noise painted at a tiny size and scaled up
// by the browser, so the upscaling itself does the smoothing.

const hash = (x: number, y: number) => {
  let h = Math.imul(x, 374761393) + Math.imul(y, 668265263);
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  return ((h ^ (h >>> 16)) >>> 0) / 4294967295;
};
const ease = (t: number) => t * t * (3 - 2 * t);
const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
const band = (lo: number, hi: number, v: number) => ease(Math.min(1, Math.max(0, (v - lo) / (hi - lo))));

function noise(x: number, y: number) {
  const xi = Math.floor(x);
  const yi = Math.floor(y);
  const u = ease(x - xi);
  const v = ease(y - yi);
  return lerp(lerp(hash(xi, yi), hash(xi + 1, yi), u), lerp(hash(xi, yi + 1), hash(xi + 1, yi + 1), u), v);
}

// The fourth octave weighed 1/16 of the field, too little to see once the canvas is scaled up.
export const OCTAVES = 3;
export const CLOUDS_PER_PIXEL = 5;
export const FRAME_MS = 125;

export function cloud(x: number, y: number) {
  let sum = 0;
  let amp = 0.5;
  let f = 1;
  for (let i = 0; i < OCTAVES; i++) {
    sum += amp * noise(x * f, y * f);
    amp /= 2;
    f *= 2.03;
  }
  return sum / (1 - 0.5 ** OCTAVES);
}

type Rgb = [number, number, number];
/** Writes the blend of a and b into out (which may be a): no array is made per pixel. */
const mixTo = (out: number[], a: ArrayLike<number>, b: Rgb, t: number) => {
  out[0] = lerp(a[0]!, b[0], t);
  out[1] = lerp(a[1]!, b[1], t);
  out[2] = lerp(a[2]!, b[2], t);
};

const PAPER: Rgb = [247, 245, 250];
const PINK: Rgb = [255, 166, 204];
const LILAC: Rgb = [218, 204, 255];
const PEACH: Rgb = [255, 216, 192];
const BLUE: Rgb = [150, 175, 255];
const ROSE: Rgb = [255, 140, 195];
const VIOLET: Rgb = [176, 140, 255];

/** The colours, for the shader in mistGl.ts. */
export const PALETTE = { PAPER, PINK, LILAC, PEACH, BLUE, ROSE, VIOLET };

export interface Spec {
  w: number;
  h: number;
  scale: number;
  /** False when paint ignores its third cloud, which is then not computed. */
  needsC: boolean;
  /** Writes red, green, blue (0..255) and alpha (0..1) into out. */
  paint: (out: number[], a: number, b: number, c: number, nx: number, ny: number) => void;
}

export const SPECS = {
  page: {
    w: 96,
    h: 60,
    scale: 2.1,
    needsC: true,
    paint: (out, a, b, c, nx, ny) => {
      const pink = band(0.44, 0.74, a) * (0.65 + 0.35 * (1 - ny));
      const lilac = band(0.44, 0.74, b) * (0.55 + 0.45 * ny);
      const peach = band(0.58, 0.82, c) * 0.6;
      mixTo(out, PAPER, PEACH, peach * (1 - pink));
      mixTo(out, out, LILAC, lilac * 0.9);
      mixTo(out, out, PINK, pink * 0.95);
      mixTo(out, out, PAPER, 0.12 * (1 - Math.abs(nx - 0.5)));
      out[3] = 1;
    },
  },
  plume: {
    w: 72,
    h: 48,
    scale: 1.7,
    needsC: false,
    paint: (out, a, b) => {
      mixTo(out, BLUE, VIOLET, band(0.3, 0.6, b));
      mixTo(out, out, ROSE, band(0.45, 0.75, a));
      out[3] = band(0.3, 0.68, a) * 0.8;
    },
  },
} satisfies Record<string, Spec>;


/** Paints the cloud field at time t (seconds) into RGBA pixel data of the spec's size. */
const px4 = [0, 0, 0, 0];
export function fillMist(d: Uint8ClampedArray, spec: Spec, t: number): void {
  for (let py = 0; py < spec.h; py++) {
    for (let px = 0; px < spec.w; px++) {
      const nx = px / spec.w;
      const ny = py / spec.h;
      const x = nx * spec.scale * 1.6;
      const y = ny * spec.scale;
      const wx = cloud(x * 0.7 + 5, y * 0.7 - t * 0.02) - 0.5;
      const wy = cloud(x * 0.7 - 3, y * 0.7 + t * 0.025 + 9) - 0.5;
      const a = cloud(x + wx * 1.4 + t * 0.05, y + wy * 1.4 - t * 0.03);
      const b = cloud(x * 0.8 + wy * 1.2 - t * 0.04 + 11, y * 0.8 + wx * 1.2 + t * 0.035 + 5);
      const c = spec.needsC ? cloud(x * 1.2 + t * 0.02 + 23, y * 1.2 - t * 0.04 + 17) : 0;
      spec.paint(px4, a, b, c, nx, ny);
      const i = (py * spec.w + px) * 4;
      d[i] = px4[0]!;
      d[i + 1] = px4[1]!;
      d[i + 2] = px4[2]!;
      d[i + 3] = px4[3]! * 255;
    }
  }
}

/** Noise lookups per second one animated canvas costs the main thread. */
export const samplesPerSecond = (spec: Spec): number => (spec.w * spec.h * (spec.needsC ? CLOUDS_PER_PIXEL : CLOUDS_PER_PIXEL - 1) * OCTAVES * 1000) / FRAME_MS;
