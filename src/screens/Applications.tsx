import { useMemo, useRef } from "react";
import { api } from "../api";
import { Icon } from "../components/Icon";
import { Empty, PageHead, Segmented, Spinner } from "../components/ui";
import { useConfirmGuard } from "../lib/confirmGuard";
import { isConfirmError, isScanExpired } from "../lib/confirm";
import { countOf, formatBytes, messageOf, sumOf, timeAgo } from "../lib/format";
import { useCachedScan, useIcons, usePatch } from "../lib/hooks";
import { createSingleFlight } from "../lib/singleFlight";
import { appWords, type AppWords } from "../lib/systemWords";
import { useApp, usePlatform } from "../store";
import type { InstalledApp } from "../types";
import "./system.css";

type Order = "name" | "size" | "used";

const PALETTE = ["#6d7bff:#a77bff", "#ff8fc8:#ff5d8f", "#3fd6a0:#5fc6ff", "#ffb547:#ff7aa8", "#1a86c4:#6d7bff", "#8a5cff:#ff8fc8", "#4f6a14:#a6c94a", "#c4253a:#ff8f5a"];

function Avatar({ app, image }: { app: InstalledApp; image: string | undefined }) {
  const [state, patch] = usePatch({ failed: false });
  if (image && !state.failed) {
    return (
      <span className="app-icon" aria-hidden="true">
        <img src={image} alt="" draggable={false} decoding="async" onError={() => patch({ failed: true })} />
      </span>
    );
  }
  const seed = [...app.name].reduce((h, ch) => (h * 31 + ch.charCodeAt(0)) >>> 0, 0);
  const [from, to] = PALETTE[seed % PALETTE.length]!.split(":");
  return (
    <span className="app-initial" style={{ background: `linear-gradient(135deg, ${from}, ${to})` }} aria-hidden="true">
      {app.name.trim().charAt(0).toUpperCase() || "?"}
    </span>
  );
}

function InlineConfirm({ words, onCancel, onGo }: { words: AppWords; onCancel: () => void; onGo: () => void }) {
  const guard = useConfirmGuard(onGo);
  return (
    <div className="inline-confirm">
      <span>{words.ask}</span>
      <button type="button" className="pbtn sm quiet" onClick={onCancel}>
        Cancel
      </button>
      <button type="button" className="pbtn sm danger" onClick={guard.onClick} onKeyDown={guard.onKeyDown} disabled={!guard.ready}>
        Uninstall
      </button>
    </div>
  );
}

function AppRow({ app, image, phase, anyBusy, words, onAsk, onCancel, onGo }: {
  app: InstalledApp; image: string | undefined; phase: "idle" | "asking" | "working"; anyBusy: boolean; words: AppWords; onAsk: () => void; onCancel: () => void; onGo: () => void;
}) {
  let action;
  if (phase === "working") {
    action = (
      <span className="locked">
        <Spinner />
        {words.working}
      </span>
    );
  } else if (phase === "asking") {
    action = <InlineConfirm words={words} onCancel={onCancel} onGo={onGo} />;
  } else if (app.canUninstall) {
    action = (
      <button type="button" className="pbtn sm" disabled={anyBusy} onClick={onAsk}>
        <Icon name="trash" size={16} />
        Uninstall
      </button>
    );
  } else {
    action = (
      <span className="locked" title={words.lockedTitle}>
        <Icon name="lock" size={14} />
        {words.locked}
      </span>
    );
  }
  return (
    <div className="row">
      <Avatar app={app} image={image} />
      <div className="row-main">
        <div className="row-title">{app.name}</div>
        <div className="row-sub">{[app.publisher, app.version && `v${app.version}`].filter(Boolean).join(" · ") || "Unknown publisher"}</div>
      </div>
      <div className="app-meta">
        <span className="row-size">{app.size ? formatBytes(app.size) : "-"}</span>
        {!!app.lastUsed && <small>Used {timeAgo(app.lastUsed)}</small>}
      </div>
      <div className="app-action">{action}</div>
    </div>
  );
}

function Applications() {
  const { notify, afterChange } = useApp();
  const words = appWords(usePlatform());
  const scan = useCachedScan("apps", (runId) => api.installedApps(runId), (e) => notify(messageOf(e, "Could not list your apps"), "err"));
  const apps = scan.data;
  const [ui, set] = usePatch<{ search: string; order: Order; asking: string | null; working: string | null }>({ search: "", order: "size", asking: null, working: null });

  const visible = useMemo(() => {
    const needle = ui.search.trim().toLowerCase();
    const alpha = (a: InstalledApp, b: InstalledApp) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
    const comparators: Record<Order, (a: InstalledApp, b: InstalledApp) => number> = {
      name: alpha,
      size: (a, b) => (b.size ?? -1) - (a.size ?? -1) || alpha(a, b),
      used: (a, b) => (b.lastUsed ?? -1) - (a.lastUsed ?? -1) || alpha(a, b),
    };
    return (apps ?? []).filter((a) => !needle || `${a.name}\n${a.publisher ?? ""}`.toLowerCase().includes(needle)).sort(comparators[ui.order]);
  }, [apps, ui.search, ui.order]);

  const once = useRef(createSingleFlight()).current;
  const removeApp = (app: InstalledApp) => once(() => removeNow(app));
  const removeNow = async (app: InstalledApp) => {
    const scanId = scan.runId;
    if (!scanId) return;
    if (scan.expired()) {
      set({ asking: null });
      notify("The app list was out of date. Look again and choose once more", "err");
      afterChange();
      void scan.rerun();
      return;
    }
    set({ asking: null, working: app.id });
    try {
      const out = await api.uninstallApp(scanId, app.id);
      notify(out.message || (out.ok ? `${app.name} removed` : `Could not uninstall ${app.name}`), out.ok ? "ok" : "err");
      if (out.ok && out.pending) {
        // Windows: the uninstaller is open and may still be cancelled, so the app stays until the list is read again
        afterChange();
      } else if (out.ok) {
        scan.replace((apps ?? []).filter((a) => a.id !== app.id));
        afterChange();
      } else {
        // a refusal may still have changed something (or the list is wrong): forget and look again
        afterChange();
        void scan.rerun();
      }
    } catch (e) {
      if (isScanExpired(e)) {
        notify("The app list was out of date. Look again and choose once more", "err");
        afterChange();
        void scan.rerun();
      } else if (isConfirmError(e)) {
        notify("The confirmation ran out. Choose Uninstall again", "err");
      } else {
        notify(messageOf(e, `Could not uninstall ${app.name}`), "err");
      }
    } finally {
      set({ working: null });
    }
  };

  // Icons load for the biggest apps first whatever the sort, so searching never restarts them.
  const icons = useIcons(useMemo(() => [...(apps ?? [])].sort((a, b) => (b.size ?? -1) - (a.size ?? -1)).map((a) => a.iconPath), [apps]));
  const footprint = sumOf((apps ?? []).map((a) => a.size ?? 0));
  const query = ui.search.trim();

  let content;
  if (!apps && (scan.broken || scan.cancelled)) {
    content = (
      <Empty icon="warning" title="Your apps could not be listed" text="Something blocked the check. Please try again shortly.">
        <button type="button" className="pbtn" onClick={() => void scan.rerun()}>
          Try again
        </button>
      </Empty>
    );
  } else if (!apps) {
    content = (
      <div className="centered">
        <Spinner />
        Gathering your apps
      </div>
    );
  } else if (visible.length === 0) {
    content = <Empty icon={query ? "search" : "apps"} title={query ? "No matches" : "No apps found"} text={query ? `Nothing matches "${query}".` : "No installed apps were found on this computer."} />;
  } else {
    content = (
      <>
        {query && (
          <div className="section-head">
            <span className="count">
              {visible.length} of {countOf(apps.length, "app", "apps")}
            </span>
          </div>
        )}
        <div className="rows">
          {visible.map((app) => (
            <AppRow
              key={app.id}
              app={app}
              image={app.iconPath ? icons[app.iconPath] : undefined}
              phase={ui.working === app.id ? "working" : ui.asking === app.id ? "asking" : "idle"}
              anyBusy={ui.working !== null}
              words={words}
              onAsk={() => set({ asking: app.id })}
              onCancel={() => set({ asking: null })}
              onGo={() => void removeApp(app)}
            />
          ))}
        </div>
      </>
    );
  }

  return (
    <div className="screen">
      <PageHead title="Applications" sub={words.sub}>
        {apps && (
          <div className="head-meta">
            <b>{apps.length}</b> apps
            {footprint > 0 && (
              <>
                <span>{"·"}</span>
                <b>{formatBytes(footprint)}</b> in total
              </>
            )}
          </div>
        )}
        <button type="button" className="pbtn sm" disabled={scan.busy || ui.working !== null} onClick={() => void scan.rerun()}>
          {scan.busy ? <Spinner /> : <Icon name="scan" />}
          {scan.busy ? "Looking" : "Rescan"}
        </button>
      </PageHead>

      <div className="toolbar rise" style={{ animationDelay: "0.06s" }}>
        <label className="search">
          <Icon name="search" size={18} />
          <input type="search" placeholder="Search by name or publisher" value={ui.search} onChange={(e) => set({ search: e.target.value })} aria-label="Search apps" />
        </label>
        <Segmented<Order>
          label="Sort apps"
          value={ui.order}
          onChange={(order) => set({ order })}
          options={[
            { value: "name", label: "Name" },
            { value: "size", label: "Size" },
            { value: "used", label: "Last used" },
          ]}
        />
      </div>

      <section className="glass pad rise" style={{ animationDelay: "0.12s" }}>
        {content}
      </section>
    </div>
  );
}

export default Applications;
