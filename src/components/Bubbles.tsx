import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import type { IconName } from "../types";
import { labelSize } from "../lib/bubbleLabel";
import { formatBytes } from "../lib/format";
import { Icon } from "./Icon";

export type BubbleDatum = {
  id: string;
  name: string;
  size: number;
  icon: IconName;
  /** Short note on the bubble: the space that can be freed there. */
  badge?: string | undefined;
  /** Real icon (data URL) to show in place of the glyph. */
  img?: string | undefined;
};

interface Placed {
  x: number;
  y: number;
  r: number;
}

const GAP = 6;
const MIN_R = 24;

/** Greedy circle packing: the biggest in the middle, the rest touching what is already there. */
function pack(radii: number[], w: number, h: number): Placed[] | null {
  const laid: Placed[] = [];
  for (const r of radii) {
    if (!laid.length) {
      laid.push({ x: w / 2, y: h / 2, r });
      continue;
    }
    let best: Placed | null = null;
    let bestCost = Infinity;
    for (const p of laid) {
      for (let a = 0; a < 360; a += 10) {
        const rad = (a * Math.PI) / 180;
        const x = p.x + Math.cos(rad) * (p.r + r + GAP);
        const y = p.y + Math.sin(rad) * (p.r + r + GAP);
        if (x - r < 0 || x + r > w || y - r < 0 || y + r > h) continue;
        if (laid.some((q) => Math.hypot(q.x - x, q.y - y) < q.r + r + GAP - 0.5)) continue;
        const cost = Math.hypot((x - w / 2) / w, (y - h / 2) / h);
        if (cost < bestCost) {
          bestCost = cost;
          best = { x, y, r };
        }
      }
    }
    if (!best) return null;
    laid.push(best);
  }
  return laid;
}

function layout(sizes: number[], w: number, h: number): Placed[] {
  const total = sizes.reduce((a, b) => a + b, 0) || 1;
  let scale = Math.sqrt((w * h * 0.5) / (Math.PI * total));
  for (let i = 0; i < 60; i++) {
    const found = pack(sizes.map((s) => Math.max(MIN_R, scale * Math.sqrt(s))), w, h);
    if (found) return found;
    scale *= 0.94;
  }
  return sizes.map(() => ({ x: w / 2, y: h / 2, r: MIN_R }));
}

const PALETTES = [
  ["#6d7bff", "#a77bff", "rgba(109,123,255,0.22)"],
  ["#3fd6a0", "#5fc6ff", "rgba(63,214,160,0.2)"],
  ["#a77bff", "#ff8fc8", "rgba(167,123,255,0.22)"],
  ["#5fc6ff", "#6d7bff", "rgba(95,198,255,0.22)"],
] as const;
const HOT = ["#ff7aa8", "#ffb347", "rgba(255,122,168,0.24)"] as const;

export function Bubbles({ items, onSelect, selectedId }: { items: BubbleDatum[]; onSelect: (id: string) => void; selectedId?: string | undefined }) {
  const box = useRef<HTMLDivElement>(null);
  const [dims, setDims] = useState({ w: 0, h: 0 });

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const read = () => setDims({ w: el.clientWidth, h: el.clientHeight });
    read();
    const watcher = new ResizeObserver(read);
    watcher.observe(el);
    return () => watcher.disconnect();
  }, []);

  const sizes = items.map((i) => i.size);
  const key = sizes.join(",");
  const spots = useMemo(() => (dims.w > 0 && dims.h > 0 ? layout(sizes, dims.w, dims.h) : []), [key, dims.w, dims.h]); // eslint-disable-line react-hooks/exhaustive-deps -- deliberate

  return (
    <div className="bubbles" ref={box}>
      {spots.map((s, i) => {
        const item = items[i];
        if (!item) return null;
        const [c1, c2, glow] = item.badge ? HOT : PALETTES[i % PALETTES.length]!;
        const style = {
          left: s.x - s.r,
          top: s.y - s.r,
          width: s.r * 2,
          height: s.r * 2,
          "--c1": c1,
          "--c2": c2,
          "--glow": glow,
          "--label": `${labelSize(item.name, s.r, s.r < 36 ? 11.5 : 12.5)}px`,
          "--chip": `${Math.max(20, Math.min(44, s.r * 0.5))}px`,
          "--spin": `${10 + (i % 4) * 3}s`,
          "--bob": `${5 + (i % 3)}s`,
          "--delay": `${-i * 0.9}s`,
        } as CSSProperties;
        return (
          <div key={item.id} className={`bubble${s.r < 36 ? " is-small" : ""}${item.badge ? " is-hot" : ""}${selectedId === item.id ? " is-picked" : ""}`} style={style}>
            <div className="bubble-float">
              <button type="button" className="bubble-disc" onClick={() => onSelect(item.id)} aria-label={`${item.name}, ${Math.round(item.size / 1024 ** 2)} MB`}>
                {s.r >= (item.badge ? 46 : 32) && (
                  <span className={`bubble-chip${item.img ? " has-img" : ""}`}>
                    {item.img ? <img src={item.img} alt="" draggable={false} /> : <Icon name={item.icon} />}
                  </span>
                )}
                <b>{item.name}</b>
                {s.r >= 28 && <span className="bubble-size">{formatBytes(item.size)}</span>}
              </button>
              {item.badge && <span className="bubble-badge">{item.badge}</span>}
            </div>
          </div>
        );
      })}
    </div>
  );
}
