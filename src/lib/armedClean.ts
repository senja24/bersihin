/** Timing rules of the Smart Scan Clean button: the first press arms it, a later press confirms. */

/** Presses this soon after arming are ignored, so a double click or double Enter cannot arm and delete. */
export const CONFIRM_DELAY_MS = 600;

/**
 * click: a pointer click. keyclick: a click the browser synthesises from Enter or Space on a focused button
 * (detail 0), which auto-repeats while the key is held. key: the global Enter handler.
 */
export type PressSource = "click" | "keyclick" | "key";

/** A click event with detail 0 did not come from a pointer. */
export const sourceOfClick = (detail: number): PressSource => (detail === 0 ? "keyclick" : "click");

export interface PressResult {
  /** When the button was armed, or null when it is not. */
  armedAt: number | null;
  /** True when this press must start the clean. */
  run: boolean;
}

/** Permanent categories always ask, whatever the confirm-before-clean preference says. */
export const needsAsk = (confirmBeforeClean: boolean | undefined, hasPermanent: boolean) => confirmBeforeClean !== false || hasPermanent;

/**
 * The global Enter handler ("key") may arm the button, and only when a confirmation is needed at all.
 * Neither it nor a keyboard click ever confirms an armed button; only a pointer click does, and only
 * CONFIRM_DELAY_MS after arming. `now` must come from a monotonic clock (performance.now()).
 */
export function pressClean(armedAt: number | null, now: number, ask: boolean, source: PressSource): PressResult {
  if (source === "key") return { armedAt: ask ? (armedAt ?? now) : armedAt, run: false };
  if (armedAt === null) return ask ? { armedAt: now, run: false } : { armedAt: null, run: true };
  if (source === "keyclick") return { armedAt, run: false };
  if (now - armedAt < CONFIRM_DELAY_MS) return { armedAt, run: false };
  return { armedAt: null, run: true };
}

/**
 * A confirm bar that has just appeared: the button under the first click must not take the second click of a
 * double click, and a held key must not repeat into it. `shownAt` and `now` come from performance.now().
 * A pointer click or a deliberate (non-repeating) keyboard click confirms once CONFIRM_DELAY_MS has passed;
 * the global Enter handler ("key") never does.
 */
export const canConfirm = (shownAt: number, now: number, source: PressSource) => source !== "key" && now - shownAt >= CONFIRM_DELAY_MS;
