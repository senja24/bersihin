import { useCallback, useEffect, useMemo, useRef, type CSSProperties, type KeyboardEvent as ReactKey } from "react";
import { api } from "../api";
import { Bubbles, type BubbleDatum } from "../components/Bubbles";
import { Icon } from "../components/Icon";
import { Mist } from "../components/Mist";
import { Capsule, Meter, Spinner, fraction } from "../components/ui";
import { pressClean, needsAsk, sourceOfClick, type PressSource } from "../lib/armedClean";
import { cleanSummary } from "../lib/cleanSummary";
import { countOf, formatBytes, isIrreversible, splitBytes, sumOf, timeAgo } from "../lib/format";
import { useCachedScan, usePatch, useTween } from "../lib/hooks";
import { hasView, useApp, useLive } from "../store";
import "./home.css";

const MIB = 1024 * 1024;
const HALF_YEAR_MS = 182 * 24 * 3600 * 1000;
const UNIT_ORDER = ["B", "KB", "MB", "GB", "TB"];

/** Formats a tweened byte count in the unit of its final value, so the unit stays put while counting. */
function tweenedParts(current: number, final: number) {
  const { value, unit } = splitBytes(final);
  const power = Math.max(0, UNIT_ORDER.indexOf(unit));
  const integral = final > 0 && !value.includes(".");
  return { value: (Math.max(0, current) / 1024 ** power).toFixed(integral ? 0 : 1), unit };
}

const badgeLabel = (bytes: number) => {
  const parts = splitBytes(bytes);
  return "-" + parts.value + parts.unit[0];
};

const SPARKS: CSSProperties[] = [18, 36, 55, 72, 88].map((pct, i) => ({
  left: `${pct}%`,
  animationDuration: `${[3.2, 2.6, 3.6, 2.9, 3.4][i]}s`,
  animationDelay: `${[0, -1.1, -2, -0.6, -1.7][i]}s`,
}));
const MOTES: CSSProperties[] = [
  [2, 18, 12, 3, 2.4, 0], [16, 6, 9, 2, 3, -0.6], [30, 20, 14, 4, 2.7, -1.2], [44, 4, 8, 1, 2.2, -0.3],
  [57, 18, 12, 3, 3.2, -1.8], [71, 8, 10, 2, 2.6, -0.9], [85, 20, 13, 4, 2.9, -2.1],
].map(([x, y, size, blur, dur, delay]) => ({
  left: `${x}%`, top: y, width: size, height: size, filter: `blur(${blur}px)`, animationDuration: `${dur}s`, animationDelay: `${delay}s`,
}));
const stagger = (s: number): CSSProperties => ({ animationDelay: `${s}s` });

type TileProps = React.HTMLAttributes<HTMLElement> & {
  kind: string;
  title: string;
  icon: "broom" | "gauge" | "shield" | "apps";
  delay: number;
  decor: React.ReactNode;
};

function Tile({ kind, title, icon, delay, decor, children, className = "", ...rest }: TileProps) {
  return (
    <article {...rest} className={`tile tile-${kind} rise ${className}`} style={stagger(delay)}>
      {decor}
      <div className="tile-top">
        <span className="tile-icon">
          <Icon name={icon} size={17} />
        </span>
        <b>{title}</b>
      </div>
      {children}
    </article>
  );
}

function SmartScan() {
  const app = useApp();
  const junkLive = useLive("junk", "clean");
  const { junk, scanning, cleaning, scannedAt, cleanedAt, prefs, reclaimable, pulse } = app;
  const [clock, setClock] = usePatch({ now: Date.now() });
  useEffect(() => {
    const t = setInterval(() => setClock({ now: Date.now() }), 30_000);
    return () => clearInterval(t);
  }, [setClock]);
  const { now } = clock;

  const tree = app.maps[""];
  const mapBusy = app.mapping.includes("");
  const mapBroken = app.mapFailed.includes("") && !mapBusy;
  // Files changing drops the maps; measure the home folder again when it is missing (a scan in progress does it itself).
  useEffect(() => {
    if (!tree && !mapBusy && !app.mapFailed.includes("") && !scanning) void app.remap();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberate
  }, [tree, mapBusy, app.mapFailed, scanning]);

  const justCleaned = !scanning && cleanedAt !== null && (scannedAt === null || cleanedAt >= scannedAt);
  const spotless = justCleaned && reclaimable === 0;
  const idleEmpty = scannedAt !== null && !scanning && !cleaning && junk !== null && reclaimable === 0;
  const safeCats = useMemo(() => (junk ?? []).filter((c) => c.safe && c.size > 0), [junk]);
  const safeIds = safeCats.map((c) => c.id);
  const itemCount = sumOf(safeCats.map((c) => c.count));
  const working = scanning || cleaning;
  const ready = !scanning && !cleaning && safeIds.length > 0;

  // The first press arms the button, a click 600 ms or more later cleans; it disarms itself after five seconds.
  const [arming, setArming] = usePatch<{ armedAt: number | null }>({ armedAt: null });
  const armed = arming.armedAt !== null;
  useEffect(() => {
    if (!armed) return;
    const timer = setTimeout(() => setArming({ armedAt: null }), 5000);
    return () => clearTimeout(timer);
  }, [armed, setArming]);

  const hasPermanent = safeCats.some(isIrreversible);
  // A changed set of categories invalidates what the user was asked to confirm.
  const safeKey = safeIds.join(",");
  useEffect(() => setArming({ armedAt: null }), [safeKey, setArming]);

  const clean = useCallback(async (source: PressSource): Promise<void> => {
    if (!ready) return;
    const press = pressClean(arming.armedAt, performance.now(), needsAsk(prefs?.confirmBeforeClean, hasPermanent), source);
    if (press.armedAt !== arming.armedAt) setArming({ armedAt: press.armedAt });
    if (!press.run) return;
    const res = await app.sweep(safeIds);
    if (!res) return;
    app.notify(cleanSummary(res));
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberate
  }, [ready, prefs, hasPermanent, arming.armedAt, app.sweep, safeIds.join(","), app.notify, setArming]);

  // Enter may arm Clean unless focus sits on something that handles Enter itself; it never confirms.
  const latestClean = useRef(clean);
  latestClean.current = clean;
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.key !== "Enter" || e.repeat || e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return;
      const el = e.target as HTMLElement | null;
      if (el && el !== document.body && el.closest("button, a, input, textarea, select, [role=button]")) return;
      void latestClean.current("key");
    };
    window.addEventListener("keydown", handler);
    return () => window.removeEventListener("keydown", handler);
  }, []);

  // Holding Enter or Space on a focused button would fire a click for every repeat.
  const blockRepeat = (e: ReactKey<HTMLElement>) => {
    if (e.repeat) e.preventDefault();
  };

  const shown = tweenedParts(useTween(reclaimable), reclaimable);

  const step = junkLive?.phase;
  let status: { text: string; tone: string };
  if (cleaning) status = { text: step ? `Cleaning · ${step}` : "Cleaning...", tone: "busy" };
  else if (scanning) status = { text: step ? `Scanning · ${step}` : "Scanning...", tone: "busy" };
  else if (justCleaned && cleanedAt !== null) status = { text: `${spotless ? "All clear" : "Cleaned"} · ${timeAgo(cleanedAt, now)}`, tone: "" };
  else if (scannedAt) status = { text: `Scan complete · ${new Date(scannedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`, tone: "" };
  else status = { text: "Not scanned yet", tone: "idle" };

  const hintText = cleaning
    ? "Deleting caches and temp files..."
    : scanning
      ? "Looking for caches and temp files..."
      : armed
        ? "Caches are deleted for good · press again"
        : itemCount > 0
          ? `${itemCount === 1 ? "1 item" : `${itemCount.toLocaleString("en-US")} items`} · ${safeIds.length} ${safeIds.length === 1 ? "group" : "groups"} ${needsAsk(prefs?.confirmBeforeClean, hasPermanent) ? " · ↵ to arm" : ""}`
          : scannedAt
            ? "Nothing safe to remove right now"
            : "Run a scan to see what can go";

  let capsule: [string, string] = ["Clean", reclaimable > 0 ? formatBytes(reclaimable) : "Nothing to clean"];
  if (cleaning) capsule = ["Cleaning", formatBytes(reclaimable)];
  else if (scanning) capsule = ["Scanning", step ?? "one moment"];
  else if (armed) capsule = ["Clean now?", `${formatBytes(reclaimable)} for good`];
  else if (idleEmpty) capsule = ["Rescan", spotless ? "all clear" : "nothing to clean"];
  else if (!scannedAt) capsule = ["Scan", "find junk"];
  const capsuleScans = idleEmpty || (!scannedAt && !scanning);

  const bubbles = useMemo((): BubbleDatum[] => {
    const sorted = (tree?.children ?? []).filter((n) => n.size > 0).sort((a, b) => b.size - a.size);
    const tag = (bytes: number) => (!justCleaned && bytes > 100 * MIB ? badgeLabel(bytes) : undefined);
    const top: BubbleDatum[] = sorted.slice(0, 7).map((n) => ({ id: n.path, name: n.name, size: n.size, icon: n.icon, badge: tag(n.reclaimable) }));
    const tail = sorted.slice(7);
    if (tail.length > 0) top.push({ id: "__more", name: `+${tail.length} more`, size: sumOf(tail.map((n) => n.size)), icon: "more", badge: tag(sumOf(tail.map((n) => n.reclaimable))) });
    return top;
  }, [tree, justCleaned]);

  const quiet = () => undefined;
  // A scan that failed or was cancelled shows a dash and a hint; opening the screen starts it again.
  const lost = (scan: { broken: boolean; cancelled: boolean; data: unknown }) => !scan.data && (scan.broken || scan.cancelled);
  // a screen the platform does not have gets no tile, and its scan never runs
  const { platform } = app;
  const showSpeed = hasView(platform, "speed");
  const showProtection = hasView(platform, "protection");
  const showApps = hasView(platform, "applications");
  const login = useCachedScan(showSpeed ? "startup" : null, () => api.startupItems(), quiet);
  const guard = useCachedScan(showProtection ? "protection" : null, () => api.protection(), quiet);
  const catalog = useCachedScan(showApps ? "apps" : null, (runId) => api.installedApps(runId), quiet);

  const logins = login.data;
  const active = (logins ?? []).filter((i) => i.enabled);
  const heavyCount = active.filter((i) => i.impact === "high").length;
  const activeShare = logins?.length ? active.length / logins.length : 0;
  const installed = catalog.data;
  const hasUsage = (installed ?? []).some((a) => a.lastUsed);
  const dormant = (installed ?? []).filter((a) => a.lastUsed && now - a.lastUsed > HALF_YEAR_MS).length;
  const appsNote = !installed ? "" : !hasUsage ? "Installed on this computer" : dormant > 0 ? `${dormant} not used for 6+ months` : "All used in the last 6 months";
  const security = guard.data;
  const securityNote = !security ? "" : security.realtime ? "Real-time protection is on" : (security.notes[0] ?? `${security.provider} is ${security.enabled ? "on" : "off"}`);
  const openProtection = () => app.navigate("protection");

  return (
    <div className="home screen">
      <div className="home-upper">
        <section className="glass hero rise" style={stagger(0.08)} aria-label="Scan result">
          <div className="hero-plume" aria-hidden="true">
            <Mist variant="plume" />
          </div>
          <div className="hero-body">
            <span className="eyebrow" title={status.text}>
              <span className={`dot ${status.tone}`} />
              <span className="eyebrow-text">{status.text}</span>
            </span>
            <h1>{justCleaned ? "Left to clean" : "Ready to reclaim"}</h1>
            <div className="hero-number" aria-live="polite">
              <span>{shown.value}</span> <small>{shown.unit}</small>
            </div>
            <p>Caches and temp files are deleted for good - they rebuild themselves. Your own files always go to the {platform.trash}.</p>
            <div className="actions">
              <Capsule
                label={capsule[0]}
                sub={capsule[1]}
                icon={!scannedAt && !scanning ? "scan" : "broom"}
                busy={scanning || cleaning}
                done={idleEmpty}
                disabled={!idleEmpty && scannedAt !== null && !ready}
                onClick={(e) => void (capsuleScans ? app.rescan() : clean(sourceOfClick(e.detail)))}
                onKeyDown={blockRepeat}
              />
              <div className="actions-text">
                <button type="button" className="text-link big" onClick={() => app.navigate("cleanup")}>
                  Review what will be removed <Icon name="arrow" size={16} />
                </button>
                <span className="hint">{hintText}</span>
                {working && (
                  <div className="hero-meter">
                    <Meter value={fraction(junkLive)} />
                  </div>
                )}
                {scanning && (
                  <button type="button" className="quiet-link" onClick={() => app.abort(["junk", "spaces"])}>
                    Cancel
                  </button>
                )}
                {armed && (
                  <button type="button" className="quiet-link" onClick={() => setArming({ armedAt: null })}>
                    Cancel
                  </button>
                )}
              </div>
            </div>
          </div>
        </section>

        <section className="glass map-card rise" style={stagger(0.16)} aria-label="Space map">
          <div className="map-top">
            <b>Space map</b>
            <button type="button" className="text-link" onClick={() => app.navigate("space-map")}>
              Open &rsaquo;
            </button>
          </div>
          <div className="map-main">
            {tree && bubbles.length > 0 && <Bubbles items={bubbles} onSelect={() => app.navigate("space-map")} />}
            {tree && bubbles.length === 0 && <div className="map-note">Nothing to show yet</div>}
            {!tree && mapBroken && (
              <div className="map-note">
                <button type="button" className="pbtn sm" onClick={() => void app.remap()}>
                  <Icon name="scan" />Measure again
                </button>
              </div>
            )}
            {!tree && !mapBroken && (
              <div className="map-skeleton" aria-hidden="true">
                {[[22, 30, 150], [64, 22, 104], [70, 64, 120], [30, 74, 84], [48, 50, 62]].map(([x, y, d], i) => (
                  <i key={i} style={{ left: `${x}%`, top: `${y}%`, width: d, height: d, animationDelay: `${-[0, 0.4, 0.9, 1.3, 0.7][i]!}s` }} />
                ))}
              </div>
            )}
          </div>
          <div className="map-bottom">
            {tree ? (
              bubbles.some((b) => b.badge) ? (
                <>
                  <i className="key" />
                  <span>A badge shows what you can free</span>
                </>
              ) : (
                <span>{justCleaned ? "No folders left to clean" : "Bubble size follows disk usage"}</span>
              )
            ) : mapBroken ? (
              <span>Measuring your folders failed</span>
            ) : (
              <>
                <Spinner size={12} />
                <span>Measuring folders...</span>
              </>
            )}
          </div>
        </section>
      </div>

      <div className="tiles" data-count={1 + Number(showSpeed) + Number(showProtection) + Number(showApps)}>
        <Tile
          kind="clean"
          title="Cleanup"
          icon="broom"
          delay={0.24}
          className={justCleaned ? "is-calm" : ""}
          decor={
            <>
              <span className="tile-glow" style={{ left: 40, top: -70, animationDuration: "5s" }} />
              {SPARKS.map((s, i) => (
                <i className="ember" key={i} style={s} />
              ))}
            </>
          }
        >
          <div className="tile-number">
            {junk ? (
              <>
                <strong>{shown.value}</strong>
                <em>{shown.unit}</em>
              </>
            ) : (
              <span className="tile-skeleton" />
            )}
          </div>
          <div className="tile-sub">{spotless ? "Caches & temp files removed" : "Caches · temp files · trash"}</div>
          <div className="tile-foot">
            {spotless ? (
              <span className="tile-pill is-done">
                <Icon name="check" size={14} />
                Removed
              </span>
            ) : (
              <button type="button" className="tile-pill" disabled={!ready} onClick={(e) => void clean(sourceOfClick(e.detail))} onKeyDown={blockRepeat}>
                {cleaning ? <><Spinner light size={14} />Removing</> : armed ? <>Confirm <Icon name="check" size={14} /></> : <>Remove <Icon name="arrow" size={14} /></>}
              </button>
            )}
          </div>
        </Tile>

        {showSpeed && (
        <Tile kind="speed" title="Speed" icon="gauge" delay={0.3} decor={<span className="tile-glow" style={{ left: 20, top: 80, animationDuration: "6s" }} />}>
          <div className="tile-number">
            {logins ? (
              <>
                <strong>{heavyCount}</strong>
                <em>{heavyCount === 1 ? "heavy app" : "heavy apps"}</em>
              </>
            ) : lost(login) ? (
              <strong>{"\u2014"}</strong>
            ) : (
              <span className="tile-skeleton" />
            )}
          </div>
          <div className="tile-sub">{logins ? `At login · ${active.length} of ${countOf(logins.length, "item", "items")} on` : lost(login) ? "Could not load - open to retry" : "Checking login items..."}</div>
          <div className="tile-foot">
            <button type="button" className={logins ? "track" : "track is-loading"} aria-label="Manage apps that start at login" onClick={() => app.navigate("speed")}>
              <span className="track-fill" style={{ width: `max(24px, calc((100% - 10px) * ${activeShare}))` }} />
              <span className="track-knob" style={{ left: `clamp(2px, calc((100% - 10px) * ${activeShare} - 25px), calc(100% - 36px))` }} />
            </button>
          </div>
        </Tile>
        )}

        {showProtection && (
        <Tile
          kind="protect"
          title="Protection"
          icon="shield"
          delay={0.36}
          className="is-tap"
          decor={<span className="tile-glow" style={{ left: 0, top: 100, animationDuration: "7s" }} />}
          role="button"
          tabIndex={0}
          aria-label="Go to Protection"
          onClick={openProtection}
          onKeyDown={(e: ReactKey) => {
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault();
              openProtection();
            }
          }}
        >
          <div className="tile-number">
            {security ? (
              <>
                <strong>{Math.round(security.score)}</strong>
                <em>/ 100</em>
              </>
            ) : lost(guard) ? (
              <strong>{"\u2014"}</strong>
            ) : (
              <span className="tile-skeleton" />
            )}
          </div>
          <div className="tile-sub">{security ? securityNote : lost(guard) ? "Could not load - open to retry" : "Checking protection..."}</div>
          <div className="tile-foot motes" aria-hidden="true">
            {MOTES.map((s, i) => (
              <i key={i} style={s} />
            ))}
          </div>
        </Tile>
        )}

        {showApps && (
        <Tile
          kind="apps"
          title="Applications"
          icon="apps"
          delay={0.42}
          decor={
            <>
              <span className="tile-glow" style={{ left: 60, top: -60, animationDuration: "6.5s" }} />
              <span className="tile-shine" />
            </>
          }
        >
          <div className="tile-number">
            {installed ? (
              <>
                <strong>{installed.length}</strong>
                <em>{installed.length === 1 ? "app" : "apps"}</em>
              </>
            ) : lost(catalog) ? (
              <strong>{"\u2014"}</strong>
            ) : (
              <span className="tile-skeleton" />
            )}
          </div>
          <div className="tile-sub">{installed ? appsNote : lost(catalog) ? "Could not load - open to retry" : "Listing installed apps..."}</div>
          <div className="tile-foot">
            <button type="button" className="tile-pill" onClick={() => app.navigate("applications")}>
              Review <Icon name="arrow" size={14} />
            </button>
          </div>
        </Tile>
        )}
      </div>
    </div>
  );
}

export default SmartScan;
