import type { ComponentType } from "react";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { Icon } from "./components/Icon";
import { Mist } from "./components/Mist";
import { Sidebar } from "./components/Sidebar";
import { Titlebar } from "./components/Titlebar";
import Applications from "./screens/Applications";
import Assistant from "./screens/Assistant";
import Cleanup from "./screens/Cleanup";
import Duplicates from "./screens/Duplicates";
import LargeFiles from "./screens/LargeFiles";
import Protection from "./screens/Protection";
import Settings from "./screens/Settings";
import SmartScan from "./screens/SmartScan";
import SpaceMap from "./screens/SpaceMap";
import Speed from "./screens/Speed";
import { AppProvider, useApp, type View } from "./store";

const PAGES: Record<View, ComponentType> = {
  "smart-scan": SmartScan, cleanup: Cleanup, protection: Protection, speed: Speed, applications: Applications,
  "space-map": SpaceMap, "large-files": LargeFiles, duplicates: Duplicates, assistant: Assistant, settings: Settings,
};

function Frame() {
  const { view, prefs, notices } = useApp();
  const wallpaper = prefs?.background ?? "mist";
  const Page = PAGES[view];
  return (
    <div className="app" data-bg={wallpaper}>
      <div className="backdrop" aria-hidden="true">
        {wallpaper === "mist" && <Mist />}
        {wallpaper === "dots" && [0, 1, 2].map((n) => <i key={n} />)}
      </div>
      <Titlebar />
      <div className="workspace">
        <Sidebar />
        <main key={view} className="main">
          <ErrorBoundary>
            <Page />
          </ErrorBoundary>
        </main>
      </div>
      <div className="toasts" role="status" aria-live="polite">
        {notices.map((n) => (
          <div key={n.id} className={n.tone === "err" ? "toast is-error" : "toast"}>
            <Icon name={n.tone === "err" ? "warning" : "check"} size={18} />
            {n.text}
          </div>
        ))}
      </div>
    </div>
  );
}

function App() {
  return (
    <AppProvider>
      <Frame />
    </AppProvider>
  );
}

export default App;
