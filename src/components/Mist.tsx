import { useEffect, useRef } from "react";
import { FRAME_MS, SPECS, fillMist, type Spec } from "../lib/mist";

export function Mist({ variant = "page" }: { variant?: keyof typeof SPECS }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx) return;
    const spec: Spec = SPECS[variant];
    const image = ctx.createImageData(spec.w, spec.h);
    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    let handle = 0;
    let last = 0;
    let t = variant === "page" ? 4 : 9;

    const draw = () => {
      fillMist(image.data, spec, t);
      ctx.putImageData(image, 0, 0);
    };

    const loop = (stamp: number) => {
      handle = requestAnimationFrame(loop);
      if (stamp - last < FRAME_MS) return;
      t += (last ? stamp - last : FRAME_MS) / 1000;
      last = stamp;
      draw();
    };
    const stop = () => {
      cancelAnimationFrame(handle);
      handle = 0;
    };
    let focused = true; // WKWebView may not report focus at mount, so only events change this
    const setFocus = (on: boolean) => () => {
      focused = on;
      sync();
    };
    const onBlur = setFocus(false);
    const onFocus = setFocus(true);
    // Animate only while the window is visible and focused; resuming restarts the clock delta.
    const sync = () => {
      const shouldRun = !still && !document.hidden && focused;
      if (shouldRun && handle === 0) {
        last = 0;
        handle = requestAnimationFrame(loop);
      } else if (!shouldRun && handle !== 0) {
        stop();
      }
    };

    draw();
    sync();
    document.addEventListener("visibilitychange", sync);
    window.addEventListener("blur", onBlur);
    window.addEventListener("focus", onFocus);
    return () => {
      stop();
      document.removeEventListener("visibilitychange", sync);
      window.removeEventListener("blur", onBlur);
      window.removeEventListener("focus", onFocus);
    };
  }, [variant]);

  return <canvas ref={canvasRef} className={`mist mist-${variant}`} width={SPECS[variant].w} height={SPECS[variant].h} aria-hidden="true" />;
}
