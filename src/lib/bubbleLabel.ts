export const LABEL_FLOOR = 9;
/** The label may use this share of the bubble's diameter (matches max-width in bubbles.css). */
export const LABEL_WIDTH_SHARE = 0.86;
/** Average glyph width of the semibold label font, in em. */
export const GLYPH_EM = 0.58;

/** Font size in px at which `name` fits a bubble of this radius: the base size, shrunk as needed down to a floor. */
export function labelSize(name: string, radius: number, base: number): number {
  if (!name) return base;
  const fit = (2 * radius * LABEL_WIDTH_SHARE) / (name.length * GLYPH_EM);
  return Math.max(LABEL_FLOOR, Math.min(base, Math.floor(fit * 10) / 10));
}
