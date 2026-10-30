/** State logic of the main view's error boundary. */

export interface BoundaryState {
  error: Error | null;
}

export type BoundaryEvent = { type: "error"; error: unknown } | { type: "reset" };

export const initialBoundary: BoundaryState = { error: null };

export function nextBoundary(_state: BoundaryState, event: BoundaryEvent): BoundaryState {
  if (event.type === "reset") return initialBoundary;
  return { error: event.error instanceof Error ? event.error : new Error(String(event.error)) };
}

/** What to tell the user: the error's message, or a plain fallback. */
export const describeError = (error: Error | null): string => error?.message || "Something went wrong while drawing this screen.";
