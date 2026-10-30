import { isDemo } from "../api";
import { useApp, useLive } from "../store";
import { Spinner } from "./ui";

/** The drag strip along the top of the window, clear of the traffic lights. */
export const Titlebar = () => {
  const { scanning, cleaning } = useApp();
  // only the work behind the busy flags: an older finished scan must not lend its phase
  const live = useLive("junk", "clean");
  const working = scanning || cleaning;
  return (
    <header className="titlebar">
      {isDemo && <span className="demo-pill no-drag">Demo data</span>}
      <div className="fill" />
      {working && (
        <span className="busy-hint">
          <Spinner size={16} />
          {live?.phase ?? (cleaning ? "Cleaning..." : "Scanning...")}
        </span>
      )}
    </header>
  );
};
