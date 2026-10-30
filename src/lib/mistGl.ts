import { OCTAVES, PALETTE, SPECS } from "./mist";

// The cloud field of mist.ts, painted by the GPU: one fragment per canvas pixel, the same noise, octaves, drift
// and colours, so the main thread no longer runs the noise. mist.ts stays as the reference and the fallback.

type Variant = keyof typeof SPECS;

const lit = (n: number) => (Number.isInteger(n) ? n.toFixed(1) : String(n));
const vec3 = ([r, g, b]: readonly number[]) => `vec3(${lit(r!)}, ${lit(g!)}, ${lit(b!)}) / 255.0`;
const palette = Object.entries(PALETTE)
  .map(([name, rgb]) => `const vec3 ${name} = ${vec3(rgb)};`)
  .join("\n");

// The hash of mist.ts in unsigned 32-bit arithmetic, which wraps like Math.imul does.
const COMMON = `#version 300 es
precision highp float;
precision highp int;
uniform float uT;
out vec4 outColor;
const int OCTAVES = ${OCTAVES};
${palette}
float ease(float t) { return t * t * (3.0 - 2.0 * t); }
float lerp(float a, float b, float t) { return a + (b - a) * t; }
float band(float lo, float hi, float v) { return ease(clamp((v - lo) / (hi - lo), 0.0, 1.0)); }
vec3 mixv(vec3 a, vec3 b, float t) { return vec3(lerp(a.x, b.x, t), lerp(a.y, b.y, t), lerp(a.z, b.z, t)); }
float hash(int x, int y) {
  uint h = uint(x) * 374761393u + uint(y) * 668265263u;
  h = (h ^ (h >> 13u)) * 1274126177u;
  return float(h ^ (h >> 16u)) / 4294967295.0;
}
float noise(float x, float y) {
  float fx = floor(x);
  float fy = floor(y);
  int xi = int(fx);
  int yi = int(fy);
  float u = ease(x - fx);
  float v = ease(y - fy);
  return lerp(lerp(hash(xi, yi), hash(xi + 1, yi), u), lerp(hash(xi, yi + 1), hash(xi + 1, yi + 1), u), v);
}
float cloud(float x, float y) {
  float sum = 0.0;
  float amp = 0.5;
  float f = 1.0;
  for (int i = 0; i < OCTAVES; i++) {
    sum += amp * noise(x * f, y * f);
    amp /= 2.0;
    f *= 2.03;
  }
  return sum / (1.0 - pow(0.5, float(OCTAVES)));
}
`;

// Per variant: the pixel size and scale of its spec, and its paint() of mist.ts.
const PAINT: Record<Variant, string> = {
  page: `
  float pink = band(0.44, 0.74, a) * (0.65 + 0.35 * (1.0 - ny));
  float lilac = band(0.44, 0.74, b) * (0.55 + 0.45 * ny);
  float peach = band(0.58, 0.82, c) * 0.6;
  vec3 col = mixv(PAPER, PEACH, peach * (1.0 - pink));
  col = mixv(col, LILAC, lilac * 0.9);
  col = mixv(col, PINK, pink * 0.95);
  col = mixv(col, PAPER, 0.12 * (1.0 - abs(nx - 0.5)));
  float alpha = 1.0;`,
  plume: `
  vec3 col = mixv(BLUE, VIOLET, band(0.3, 0.6, b));
  col = mixv(col, ROSE, band(0.45, 0.75, a));
  float alpha = band(0.3, 0.68, a) * 0.8;`,
};

/** The fragment shader of a variant; row 0 of the image is the top row, as in fillMist. */
export function fragmentSource(variant: Variant): string {
  const spec = SPECS[variant];
  return `${COMMON}
const vec2 SIZE = vec2(${lit(spec.w)}, ${lit(spec.h)});
const float SCALE = ${lit(spec.scale)};
void main() {
  float px = floor(gl_FragCoord.x);
  float py = SIZE.y - 1.0 - floor(gl_FragCoord.y);
  float nx = px / SIZE.x;
  float ny = py / SIZE.y;
  float x = nx * SCALE * 1.6;
  float y = ny * SCALE;
  float t = uT;
  float wx = cloud(x * 0.7 + 5.0, y * 0.7 - t * 0.02) - 0.5;
  float wy = cloud(x * 0.7 - 3.0, y * 0.7 + t * 0.025 + 9.0) - 0.5;
  float a = cloud(x + wx * 1.4 + t * 0.05, y + wy * 1.4 - t * 0.03);
  float b = cloud(x * 0.8 + wy * 1.2 - t * 0.04 + 11.0, y * 0.8 + wx * 1.2 + t * 0.035 + 5.0);
  float c = ${spec.needsC ? "cloud(x * 1.2 + t * 0.02 + 23.0, y * 1.2 - t * 0.04 + 17.0)" : "0.0"};
${PAINT[variant]}
  outColor = vec4(col * alpha, alpha);
}
`;
}

const VERTEX = `#version 300 es
void main() {
  vec2 p = vec2(float((gl_VertexID << 1) & 2), float(gl_VertexID & 2));
  gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}
`;

export interface MistGl {
  /** Paints the field at time t (seconds). */
  draw: (t: number) => void;
  /** Frees the program; the context too once the canvas has left the page. */
  dispose: () => void;
}

function compile(gl: WebGL2RenderingContext, variant: Variant): WebGLProgram | null {
  const program = gl.createProgram();
  const shaders = [gl.createShader(gl.VERTEX_SHADER), gl.createShader(gl.FRAGMENT_SHADER)];
  const sources = [VERTEX, fragmentSource(variant)];
  const free = () => {
    shaders.forEach((s) => s && gl.deleteShader(s));
    gl.deleteProgram(program);
  };
  if (!program || shaders.some((s) => !s)) return free(), null;
  shaders.forEach((s, i) => {
    gl.shaderSource(s!, sources[i]!);
    gl.compileShader(s!);
    gl.attachShader(program, s!);
  });
  gl.linkProgram(program);
  const ok = gl.getProgramParameter(program, gl.LINK_STATUS);
  shaders.forEach((s) => gl.deleteShader(s!));
  if (!ok) return gl.deleteProgram(program), null;
  return program;
}

// A canvas keeps the kind of context it first gave out, so whether the shader works is learnt on a spare canvas.
const works = new Map<Variant, boolean>();
function supported(variant: Variant): boolean {
  let ok = works.get(variant);
  if (ok === undefined) {
    const spare = document.createElement("canvas").getContext("webgl2");
    const program = spare && compile(spare, variant);
    ok = !!program;
    if (spare) {
      if (program) spare.deleteProgram(program);
      spare.getExtension("WEBGL_lose_context")?.loseContext();
    }
    works.set(variant, ok);
  }
  return ok;
}

/**
 * Paints the variant's cloud field on the canvas with WebGL2, or returns null when that is not available.
 * A context can be lost (sleep, GPU switch, driver reset): drawing then pauses and the shader is built again when the
 * context comes back. When that fails, or the canvas gave out a context that is already unusable, onFail is called:
 * such a canvas can no longer give a 2D context, so the caller has to continue on a fresh canvas.
 */
export function createMistGl(canvas: HTMLCanvasElement, variant: Variant, onFail: () => void = () => undefined): MistGl | null {
  if (typeof WebGL2RenderingContext === "undefined" || !supported(variant)) return null;
  const gl = canvas.getContext("webgl2", { alpha: true, antialias: false, depth: false, stencil: false, premultipliedAlpha: true, powerPreference: "low-power" });
  if (!gl) return null;
  let program: WebGLProgram | null = null;
  let time: WebGLUniformLocation | null = null;
  let lastT: number | null = null;
  let lost = false;
  const paint = (t: number) => {
    gl.uniform1f(time, t);
    gl.drawArrays(gl.TRIANGLES, 0, 3);
  };
  const build = () => {
    program = compile(gl, variant);
    if (!program) return false;
    time = gl.getUniformLocation(program, "uT");
    gl.useProgram(program);
    gl.viewport(0, 0, canvas.width, canvas.height);
    return true;
  };
  const onLost = (e: Event) => {
    e.preventDefault(); // without this the context is never restored
    lost = true;
  };
  const onRestored = () => {
    if (!build()) return onFail();
    lost = false;
    if (lastT !== null) paint(lastT);
  };
  if (gl.isContextLost() || !build()) {
    onFail();
    return null;
  }
  canvas.addEventListener("webglcontextlost", onLost);
  canvas.addEventListener("webglcontextrestored", onRestored);
  return {
    draw: (t) => {
      lastT = t;
      if (!lost) paint(t);
    },
    dispose: () => {
      canvas.removeEventListener("webglcontextlost", onLost);
      canvas.removeEventListener("webglcontextrestored", onRestored);
      if (program) gl.deleteProgram(program);
      // React may run an effect's cleanup and setup again on the same canvas; only a canvas that left the page is done with.
      if (!canvas.isConnected) gl.getExtension("WEBGL_lose_context")?.loseContext();
    },
  };
}
