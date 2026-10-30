import { api } from "../api";

// Whether the window is in front. Animations run only then: a window behind other apps is not seen, and the
// canvas and CSS loops would burn CPU and GPU for nothing. The DOM events are not enough: WKWebView sends no
// blur to a page whose window was never focused, so the native side reports the focus too. Once the native side has
// reported, it alone decides: a DOM blur also comes when focus moves to a native control while the window stays key.
let focused = true;
let native = false;
const listeners = new Set<() => void>();

export const isActive = () => !document.hidden && focused;

function changed() {
  document.documentElement.toggleAttribute("data-idle", !isActive());
  listeners.forEach((fn) => fn());
}
function setFocused(on: boolean) {
  if (focused === on) return;
  focused = on;
  changed();
}

/** Calls fn whenever the window gets in front or goes behind; returns the unsubscribe function. */
export function onActivity(fn: () => void): () => void {
  listeners.add(fn);
  return () => void listeners.delete(fn);
}

window.addEventListener("blur", () => native || setFocused(false));
window.addEventListener("focus", () => native || setFocused(true));
document.addEventListener("visibilitychange", changed);
api.onWindowFocus((on) => {
  native = true;
  setFocused(on);
});
