import { useEffect, useRef, useState } from "react";
import { isActive, onActivity } from "../lib/activity";
import { createMistGl } from "../lib/mistGl";
import { FRAME_MS, SPECS, fillMist, type Spec } from "../lib/mist";

export function Mist({ variant = "page" }: { variant?: keyof typeof SPECS }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  // Set when the GPU path gave up for good: a canvas that handed out WebGL cannot give 2D, so a new canvas takes over.
  const [cpuOnly, setCpuOnly] = useState(false);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    // The GPU paints the field when it can; the CPU code of lib/mist.ts is the fallback.
    const gpu = cpuOnly ? null : createMistGl(canvas, variant, () => setCpuOnly(true));
    const ctx = gpu ? null : canvas.getContext("2d");
    if (!gpu && !ctx) return;
    const spec: Spec = SPECS[variant];
    const image = ctx?.createImageData(spec.w, spec.h);
    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    let handle = 0;
    let last = 0;
    let t = variant === "page" ? 4 : 9;

    const draw = () => {
      if (gpu) return gpu.draw(t);
      fillMist(image!.data, spec, t);
      ctx!.putImageData(image!, 0, 0);
    };

    // A timer, not requestAnimationFrame: a rAF loop wakes the page 60 times a second even when it only skips frames.
    const loop = () => {
      const stamp = performance.now();
      t += (last ? stamp - last : FRAME_MS) / 1000;
      last = stamp;
      draw();
      handle = window.setTimeout(loop, FRAME_MS);
    };
    const stop = () => {
      clearTimeout(handle);
      handle = 0;
    };
    // Animate only while the window is in front; resuming restarts the clock delta.
    const sync = () => {
      const shouldRun = !still && isActive();
      if (shouldRun && handle === 0) {
        last = 0;
        handle = window.setTimeout(loop, FRAME_MS);
      } else if (!shouldRun && handle !== 0) {
        stop();
      }
    };

    draw();
    sync();
    const off = onActivity(sync);
    return () => {
      stop();
      off();
      gpu?.dispose();
    };
  }, [variant, cpuOnly]);

  return <canvas key={cpuOnly ? "cpu" : "gpu"} ref={canvasRef} className={`mist mist-${variant}`} width={SPECS[variant].w} height={SPECS[variant].h} aria-hidden="true" />;
}
