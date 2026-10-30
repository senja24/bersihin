/**
 * Lets only the newest piece of async work report back. start() hands out a ticket, cancel() invalidates every ticket
 * handed out so far, and a result is shown only while its ticket is still current.
 */
export function createStaleGuard() {
  let generation = 0;
  return {
    start: () => generation,
    cancel: () => void generation++,
    isCurrent: (ticket: number) => ticket === generation,
  };
}
