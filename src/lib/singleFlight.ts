/** Lets one async action run at a time: calls made while it runs are ignored. Closes the gap before a re-render disables the button. */
export function createSingleFlight() {
  let busy = false;
  return async (task: () => Promise<void>): Promise<void> => {
    if (busy) return;
    busy = true;
    try {
      await task();
    } finally {
      busy = false;
    }
  };
}
