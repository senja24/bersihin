// Tells the page when a backend run is over, so its last progress report can be dropped from the screen.
const listeners = new Set<(runId: string) => void>();

export const onRunEnd = (fn: (runId: string) => void): (() => void) => {
  listeners.add(fn);
  return () => void listeners.delete(fn);
};

export const runEnded = (runId: string): void => listeners.forEach((fn) => fn(runId));
