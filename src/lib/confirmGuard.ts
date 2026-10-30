import { useEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from "react";
import { CONFIRM_DELAY_MS, canConfirm, sourceOfClick } from "./armedClean";

/**
 * Guards the confirm button of a bar that just appeared. Call it in the component that mounts with the bar.
 * ready turns true after CONFIRM_DELAY_MS (for a subtle disabled look); onClick also checks the monotonic clock itself.
 */
export function useConfirmGuard(onConfirm: () => void) {
  const shownAt = useRef(performance.now());
  const [ready, setReady] = useState(false);
  useEffect(() => {
    const timer = setTimeout(() => setReady(true), CONFIRM_DELAY_MS);
    return () => clearTimeout(timer);
  }, []);
  return {
    ready,
    onClick: (e: MouseEvent<HTMLButtonElement>) => {
      if (canConfirm(shownAt.current, performance.now(), sourceOfClick(e.detail))) onConfirm();
    },
    // Holding Enter or Space on the focused button would fire a click for every repeat.
    onKeyDown: (e: KeyboardEvent<HTMLButtonElement>) => {
      if (e.repeat) e.preventDefault();
    },
  };
}
