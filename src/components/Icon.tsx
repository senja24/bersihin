import type { CSSProperties } from "react";
import type { IconName } from "../types";

// Hand-drawn on a 24 grid. f: soft tinted body, s: outline, d: solid dots, a: accent shape.
interface Glyph {
  f?: string;
  s?: string;
  d?: string;
  a?: string;
}

const dot = (x: number, y: number, r = 1.4) => `M${x - r} ${y}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0z`;
const ring = "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z";
const bubble = "M5 4h14a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1h-8l-4 4v-4H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z";
const page = "M6 3h8l4 4v14H6z";
const folder = "M3.5 7a1.5 1.5 0 0 1 1.5-1.5h4l2 2.5h8a1.5 1.5 0 0 1 1.5 1.5v8.5a1.5 1.5 0 0 1-1.5 1.5H5A1.5 1.5 0 0 1 3.5 18z";

const GLYPHS: Record<IconName, Glyph> = {
  scan: { f: ring, s: `${ring}M12 7.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9z`, d: dot(12, 12, 1.2) },
  broom: { f: "M10 12l4 4c-2 4-4.5 4-10 4 0-3.5 2-6 6-8z", s: "M19 4l-6.5 6.5M10.5 8.5l5 5M4 20c0-3.5 2-6 6-8l4 4c-2 4-4.5 4-10 4z" },
  shield: { f: "M12 3l7 3v5.5c0 4.5-3 8-7 9.5-4-1.5-7-5-7-9.5V6z", s: "M12 3l7 3v5.5c0 4.5-3 8-7 9.5-4-1.5-7-5-7-9.5V6zM9 12l2 2 4-4" },
  gauge: { f: "M4.5 17a8.5 8.5 0 1 1 15 0z", s: "M4.5 17a8.5 8.5 0 1 1 15 0M12 14l4-4.5", d: dot(12, 14.2, 1.3) },
  apps: { f: "M4 4h6v6H4z", s: "M4 4h6v6H4zM4 14h6v6H4zM14 14h6v6h-6z", a: "M17 4a3 3 0 1 0 0 6 3 3 0 0 0 0-6z" },
  bubbles: { f: "M9 8a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9z", s: "M9 8a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9zM17.5 4a2.5 2.5 0 1 0 0 5 2.5 2.5 0 0 0 0-5zM17 14.5a3 3 0 1 0 0 6 3 3 0 0 0 0-6z" },
  file: { f: page, s: `${page}M14 3v4h4M9 12h6M9 16h6` },
  dupes: { f: "M8 8h11v11H8z", s: "M8 8h11v11H8zM5 16V5h11M13.5 11.5v5M11 14h5" },
  history: { s: "M4 12a8 8 0 1 0 2.5-5.8M4 4v4h4M12 8v4l3 2" },
  assist: { f: bubble, s: `${bubble}M12 7.5v5M9.5 10h5` },
  user: { f: "M12 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8z", s: "M12 4a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM4.5 20c.8-3.6 3.8-5.5 7.5-5.5s6.7 1.9 7.5 5.5" },
  game: { f: "M7 8h10a4 4 0 0 1 4 4v1a3.5 3.5 0 0 1-6 2.5L14 14h-4l-1 1.5A3.5 3.5 0 0 1 3 13v-1a4 4 0 0 1 4-4z", s: "M7 8h10a4 4 0 0 1 4 4v1a3.5 3.5 0 0 1-6 2.5L14 14h-4l-1 1.5A3.5 3.5 0 0 1 3 13v-1a4 4 0 0 1 4-4zM8 10.5v3M6.5 12h3", d: `${dot(16, 11.3, 0.9)}${dot(18, 13, 0.9)}` },
  down: { s: "M12 4v11M7.5 10.5L12 15l4.5-4.5M5 19h14" },
  chip: { f: "M7 7h10v10H7z", s: "M7 7h10v10H7zM10 10h4v4h-4zM10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4" },
  more: { d: `${dot(6, 12, 1.7)}${dot(12, 12, 1.7)}${dot(18, 12, 1.7)}` },
  check: { s: "M5 12.5l4.5 4.5L19 7.5" },
  arrow: { s: "M5 12h14M13 6l6 6-6 6" },
  heart: { f: "M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.5A4 4 0 0 1 19 10c0 5.6-7 10-7 10z", s: "M12 20s-7-4.4-7-10a4 4 0 0 1 7-2.5A4 4 0 0 1 19 10c0 5.6-7 10-7 10z" },
  chat: { f: bubble, s: `${bubble}M8 9h8M8 12h5` },
  gear: { s: "M4 8h9M17 8h3M4 16h3M11 16h9M15 6a2 2 0 1 0 0 4 2 2 0 0 0 0-4zM9 14a2 2 0 1 0 0 4 2 2 0 0 0 0-4z" },
  trash: { f: "M7 7l1 13h8l1-13z", s: "M5 7h14M9 7V4.5h6V7M7 7l1 13h8l1-13M10 11v6M14 11v6" },
  folder: { f: folder, s: folder },
  photo: { f: "M4 5h16v14H4z", s: "M4 5h16v14H4zM4 16l5-5 4 4 3-3 4 4", d: dot(15.5, 9.5, 1.2) },
  video: { f: "M4 6h11v12H4z", s: "M4 6h11v12H4zM15 10l5-3v10l-5-3z" },
  music: { s: "M9 18V6l10-2v12M9 18a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0zM19 16a2.5 2.5 0 1 1-5 0 2.5 2.5 0 0 1 5 0z" },
  doc: { f: page, s: `${page}M14 3v4h4M9 11h6M9 14h6M9 17h3` },
  code: { s: "M9 7l-5 5 5 5M15 7l5 5-5 5M13.5 5l-3 14" },
  cloud: { f: "M7 18a4 4 0 0 1-.5-8A5.5 5.5 0 0 1 17 8.5 4.8 4.8 0 0 1 17.5 18z", s: "M7 18a4 4 0 0 1-.5-8A5.5 5.5 0 0 1 17 8.5 4.8 4.8 0 0 1 17.5 18z" },
  mail: { f: "M4 6h16v12H4z", s: "M4 6h16v12H4zM4 7l8 6 8-6" },
  globe: { f: ring, s: `${ring}M3 12h18M12 3c2.5 2.7 3.5 5.7 3.5 9S14.5 18.3 12 21M12 3C9.5 5.7 8.5 8.7 8.5 12S9.5 18.3 12 21` },
  layers: { f: "M12 4l9 5-9 5-9-5z", s: "M12 4l9 5-9 5-9-5zM3 13l9 5 9-5" },
  lock: { f: "M6 11h12v9H6z", s: "M6 11h12v9H6zM8.5 11V8a3.5 3.5 0 0 1 7 0v3M12 14.5v2.5" },
  power: { s: "M12 3v8M6.5 6.8a8 8 0 1 0 11 0" },
  search: { s: "M10.5 4a6.5 6.5 0 1 0 0 13 6.5 6.5 0 0 0 0-13zM15.5 15.5L20 20" },
  close: { s: "M6 6l12 12M18 6L6 18" },
  plus: { s: "M12 5v14M5 12h14" },
  reveal: { s: "M14 4h6v6M20 4l-9 9M18 14v5H5V6h5" },
  spark: { f: "M12 3c.6 4.6 2.4 6.4 7 7-4.6.6-6.4 2.4-7 7-.6-4.6-2.4-6.4-7-7 4.6-.6 6.4-2.4 7-7z", s: "M12 3c.6 4.6 2.4 6.4 7 7-4.6.6-6.4 2.4-7 7-.6-4.6-2.4-6.4-7-7 4.6-.6 6.4-2.4 7-7z" },
  warning: { f: "M12 4l9 16H3z", s: "M12 4l9 16H3zM12 10v4", d: dot(12, 17, 0.9) },
  clock: { f: ring, s: `${ring}M12 7v5l3 2` },
};

export function Icon({ name, size, className, style }: { name: IconName; size?: number; className?: string; style?: CSSProperties }) {
  const g = GLYPHS[name];
  return (
    <svg className={`icon${className ? ` ${className}` : ""}`} viewBox="0 0 24 24" width={size ?? 20} height={size ?? 20} aria-hidden="true" style={style}>
      {g.f && <path className="icon-body" d={g.f} />}
      {g.s && <path className="icon-line" d={g.s} />}
      {g.d && <path className="icon-dot" d={g.d} />}
      {g.a && <path className="icon-accent" d={g.a} />}
    </svg>
  );
}

const BRAND_STOPS: [string, string][] = [["0%", "#2b4cff"], ["55%", "#8a5cff"], ["100%", "#ff5d8f"]];

/** The blue, violet and pink gradient used by the mark and the protection ring. */
export function BrandGradient({ id }: { id: string }) {
  return (
    <linearGradient id={id} x1="0" y1="0" x2="1" y2="1">
      {BRAND_STOPS.map(([at, color]) => (
        <stop key={at} offset={at} stopColor={color} />
      ))}
    </linearGradient>
  );
}

/** Bersihin's mark: a soft gradient bubble with a wipe across it and a spark. */
export function BrandMark(props: { size?: number }) {
  const size = props.size ?? 30;
  return (
    <svg viewBox="0 0 32 32" width={size} height={size} aria-hidden="true">
      <defs>
        <BrandGradient id="brand-fill" />
      </defs>
      <circle cx="16" cy="16" r="14" fill="url(#brand-fill)" />
      <path d="M8.5 18.5c3.4 4 9.4 4 13.5-.5" fill="none" stroke="#fff" strokeWidth="2.4" strokeLinecap="round" />
      <path d="M20.5 7.5l.9 2.4 2.4.9-2.4.9-.9 2.4-.9-2.4-2.4-.9 2.4-.9z" fill="#fff" />
    </svg>
  );
}
