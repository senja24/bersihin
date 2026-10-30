import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { createMistGl } from "./mistGl";

// A stand-in for WebGL2 that records what the painter asks of it. A real context is lost and restored by the
// browser; here the test fires the same two events on the canvas.
function fakeGl(opts: { linkOk: () => boolean }) {
  const calls: string[] = [];
  let programs = 0;
  const gl = {
    VERTEX_SHADER: 1, FRAGMENT_SHADER: 2, LINK_STATUS: 3, TRIANGLES: 4,
    createProgram: () => (calls.push("createProgram"), { id: ++programs }),
    createShader: () => ({}),
    shaderSource: () => undefined,
    compileShader: () => undefined,
    attachShader: () => undefined,
    linkProgram: () => undefined,
    getProgramParameter: () => opts.linkOk(),
    deleteShader: () => undefined,
    deleteProgram: () => calls.push("deleteProgram"),
    getUniformLocation: () => ({}),
    useProgram: () => calls.push("useProgram"),
    viewport: () => calls.push("viewport"),
    uniform1f: (_: unknown, t: number) => calls.push(`uniform ${t}`),
    drawArrays: () => calls.push("draw"),
    isContextLost: () => false,
    getExtension: () => null,
  };
  return { gl, calls };
}

function fakeCanvas(gl: unknown) {
  const listeners = new Map<string, Set<(e: Event) => void>>();
  const canvas = {
    width: 96, height: 60, isConnected: true,
    getContext: () => gl,
    addEventListener: (n: string, f: (e: Event) => void) => void (listeners.get(n) ?? listeners.set(n, new Set()).get(n)!).add(f),
    removeEventListener: (n: string, f: (e: Event) => void) => void listeners.get(n)?.delete(f),
  };
  const fire = (n: string) => {
    let prevented = false;
    listeners.get(n)?.forEach((f) => f({ preventDefault: () => (prevented = true) } as unknown as Event));
    return prevented;
  };
  return { canvas: canvas as unknown as HTMLCanvasElement, fire, count: (n: string) => listeners.get(n)?.size ?? 0 };
}

const g = globalThis as Record<string, unknown>;
const saved = { document: g.document, WebGL2RenderingContext: g.WebGL2RenderingContext };
let probeLinks = true;
beforeAll(() => {
  g.WebGL2RenderingContext = class {};
  g.document = { createElement: () => ({ getContext: () => fakeGl({ linkOk: () => probeLinks }).gl }) };
});
afterAll(() => {
  g.document = saved.document;
  g.WebGL2RenderingContext = saved.WebGL2RenderingContext;
});

describe("mist on a lost WebGL context", () => {
  test("drawing pauses while lost, then the shader is rebuilt and the last frame painted again", () => {
    const { gl, calls } = fakeGl({ linkOk: () => true });
    const { canvas, fire } = fakeCanvas(gl);
    let failed = 0;
    const mist = createMistGl(canvas, "page", () => failed++)!;
    mist.draw(1);
    expect(calls.filter((c) => c === "draw")).toHaveLength(1);

    expect(fire("webglcontextlost")).toBe(true); // preventDefault: the browser may now restore it
    calls.length = 0;
    mist.draw(2);
    expect(calls).toEqual([]);

    fire("webglcontextrestored");
    expect(calls).toEqual(["createProgram", "useProgram", "viewport", "uniform 2", "draw"]);
    calls.length = 0;
    mist.draw(3);
    expect(calls).toEqual(["uniform 3", "draw"]);
    expect(failed).toBe(0);
  });

  test("a restore whose shader no longer builds asks for the fresh-canvas fallback", () => {
    let ok = true;
    const { gl, calls } = fakeGl({ linkOk: () => ok });
    const { canvas, fire } = fakeCanvas(gl);
    let failed = 0;
    const mist = createMistGl(canvas, "plume", () => failed++)!;
    fire("webglcontextlost");
    ok = false;
    fire("webglcontextrestored");
    expect(failed).toBe(1);
    calls.length = 0;
    mist.draw(5);
    expect(calls).toEqual([]);
  });

  test("a context that is already lost when handed out asks for the fallback", () => {
    const { gl } = fakeGl({ linkOk: () => true });
    gl.isContextLost = () => true;
    const { canvas, count } = fakeCanvas(gl);
    let failed = 0;
    expect(createMistGl(canvas, "page", () => failed++)).toBeNull();
    expect(failed).toBe(1);
    expect(count("webglcontextlost")).toBe(0);
  });

  test("dispose removes both listeners", () => {
    const { gl } = fakeGl({ linkOk: () => true });
    const { canvas, count } = fakeCanvas(gl);
    const mist = createMistGl(canvas, "page")!;
    expect(count("webglcontextlost") + count("webglcontextrestored")).toBe(2);
    mist.dispose();
    expect(count("webglcontextlost") + count("webglcontextrestored")).toBe(0);
  });
});
