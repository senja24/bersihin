import { formatBytes } from "../lib/format";
import { hasView, useApp, type View } from "../store";
import type { DiskInfo, IconName } from "../types";
import { BrandMark, Icon } from "./Icon";

type Entry = [view: View, label: string, icon: IconName];

const TOP: Entry[] = [
  ["smart-scan", "Smart Scan", "scan"],
  ["cleanup", "Cleanup", "broom"],
  ["protection", "Protection", "shield"],
  ["speed", "Speed", "gauge"],
  ["applications", "Applications", "apps"],
];
const FILE_TOOLS: Entry[] = [
  ["space-map", "Space Map", "bubbles"],
  ["large-files", "Large Files", "file"],
  ["duplicates", "Duplicates", "dupes"],
  ["assistant", "Assistant", "assist"],
];

function DiskMeter({ disk, junkBytes }: { disk: DiskInfo; junkBytes: number }) {
  const usedPct = disk.total > 0 ? Math.min(100, (disk.used / disk.total) * 100) : 0;
  const junkPct = disk.total > 0 ? Math.min(usedPct, (junkBytes / disk.total) * 100) : 0;
  return (
    <div className="disk-row" title={`${disk.label}: ${formatBytes(disk.free)} free of ${formatBytes(disk.total)}`}>
      <div className="disk-line">
        <span>{disk.label}</span>
        <span>
          {formatBytes(disk.used)} / {formatBytes(disk.total)}
        </span>
      </div>
      <div className="disk-bar">
        <span className="used" style={{ width: `${usedPct - junkPct}%` }} />
        <span style={{ width: `${junkPct}%` }} className="junk" />
      </div>
    </div>
  );
}

export const Sidebar = () => {
  const app = useApp();
  const { view, navigate, reclaimable } = app;

  const entry = ([id, label, icon]: Entry) => (
    <button
      type="button"
      key={id}
      data-testid={`nav-${id}`}
      className={view === id ? "nav-item is-on" : "nav-item"}
      aria-current={view === id ? "page" : undefined}
      onClick={() => navigate(id)}
    >
      <Icon name={icon} />
      {label}
      {id === "cleanup" && reclaimable > 0 ? <span className="nav-count">{formatBytes(reclaimable)}</span> : null}
    </button>
  );

  const drives = app.volumes?.length ? app.volumes : app.primary ? [app.primary] : [];
  const home = drives.find((d) => d.system) ?? drives[0];
  let summary = "Measuring...";
  if (home) {
    if (reclaimable > 0) summary = `${formatBytes(reclaimable)} can be reclaimed${drives.length > 1 ? ` on ${home.label}` : ""}`;
    else if (drives.length > 1) summary = `${formatBytes(drives.reduce((n, d) => n + d.free, 0))} free on ${drives.length} drives`;
    else summary = `${formatBytes(home.free)} free`;
  }

  return (
    <nav className="sidebar rise" aria-label="Sections">
      <div className="brand">
        <BrandMark />
        <span>Bersihin</span>
      </div>
      {TOP.filter(([id]) => hasView(app.platform, id)).map(entry)}
      <div className="nav-group">FILES</div>
      {FILE_TOOLS.map(entry)}

      <div className="sidebar-foot">
        <div className="disk-card">
          {drives.length === 0 && (
            <div className="disk-row" aria-busy="true">
              <div className="disk-line">
                <span>Disk</span>
                <span>...</span>
              </div>
              <div className="disk-bar" />
            </div>
          )}
          {drives.map((d) => (
            <DiskMeter key={d.mount} disk={d} junkBytes={d.mount === home?.mount ? reclaimable : 0} />
          ))}
          <div className="disk-note">{summary}</div>
        </div>
        <div className="sidebar-actions">
          <button
            type="button"
            data-testid="nav-settings"
            className={view === "settings" ? "nav-item nav-icon is-on" : "nav-item nav-icon"}
            aria-label="Settings"
            title="Settings"
            aria-current={view === "settings" ? "page" : undefined}
            onClick={() => navigate("settings")}
          >
            <Icon name="gear" />
          </button>
        </div>
      </div>
    </nav>
  );
};
