import { useEffect, useMemo } from "react";
import { api } from "../api";
import { Bubbles, type BubbleDatum } from "../components/Bubbles";
import { Icon } from "../components/Icon";
import { BigNumber, Empty, PageHead, Segmented, Spinner, Waiting, fraction } from "../components/ui";
import { formatBytes, messageOf, splitBytes, tildePath } from "../lib/format";
import { useIcons, usePatch } from "../lib/hooks";
import { useApp, useLiveRun } from "../store";
import type { DiskInfo, SpaceNode } from "../types";
import "./lists.css";

const TOP_N = 12;
const WORTH_NOTING = 50 * 1024 * 1024;
const OVERFLOW = "__overflow";
const HOME_KEY = "~";
// Entries such as "N more" share their parent's path, so the name joins the key.
const keyOf = (n: SpaceNode) => `${n.path}|${n.name}`;

interface Source {
  key: string;
  label: string;
  disk?: DiskInfo;
}

// The source picked last stays selected when the screen is revisited.
const remembered = { source: HOME_KEY };

function SourceNote({ src, home }: { src: Source; home: string | undefined }) {
  if (!src.disk) return <span className="source-note">{home ? `Your home folder · ${home}` : "Your home folder"}</span>;
  const d = src.disk;
  const usedShare = d.total > 0 ? Math.min(100, (d.used / d.total) * 100) : 0;
  return (
    <span className="source-note" title={`${d.label}: ${formatBytes(d.free)} free`}>
      <span className="disk-bar mini" aria-hidden="true">
        <span style={{ width: `${usedShare}%` }} className="used" />
      </span>
      {formatBytes(d.used)} of {formatBytes(d.total)} used{d.system ? " · system volume" : ""}
    </span>
  );
}

interface SideProps {
  folder: SpaceNode;
  title: string;
  kids: SpaceNode[];
  focus: string | null;
  icons: Record<string, string>;
  home: string | undefined;
  onOpen: (n: SpaceNode) => void;
  onReveal: (path: string) => void;
  onReview: () => void;
}

function SidePanel({ folder, title, kids, focus, icons, home, onOpen, onReveal, onReview }: SideProps) {
  const shownKids = kids.slice(0, 80);
  return (
    <aside className="glass side rise" style={{ animationDelay: "0.12s" }} aria-label="Folder contents">
      <div className="side-head">
        <div className="row-main">
          <h2>{title}</h2>
          <p title={folder.path}>{tildePath(folder.path, home)}</p>
        </div>
        <button type="button" className="icon-btn" title="Show in Finder" aria-label="Show in Finder" onClick={() => onReveal(folder.path)}>
          <Icon name="reveal" size={16} />
        </button>
      </div>
      {folder.reclaimable >= WORTH_NOTING && (
        <div className="hint-card">
          <Icon name="spark" size={16} />
          <span className="row-main">
            Roughly <b>{formatBytes(folder.reclaimable)}</b> in here can be cleared: caches and stale downloads.
          </span>
          <button type="button" className="pbtn sm" onClick={onReview}>
            Review
          </button>
        </div>
      )}
      {kids.length === 0 ? (
        <Empty title="Empty folder" icon="folder" />
      ) : (
        <div className="rows side-rows">
          {shownKids.map((c) => {
            const expandable = (c.children?.length ?? 0) > 0;
            const ratio = folder.size > 0 ? Math.max(1, Math.round((c.size / folder.size) * 100)) : 0;
            const img = icons[c.path];
            const cleanable = c.reclaimable >= WORTH_NOTING;
            const classes = ["row", expandable && "is-drill", focus === keyOf(c) && "is-picked"].filter(Boolean).join(" ");
            return (
              <div
                id={`node-${keyOf(c)}`}
                key={keyOf(c)}
                className={classes}
                role={expandable ? "button" : undefined}
                tabIndex={expandable ? 0 : undefined}
                onClick={() => onOpen(c)}
                onKeyDown={(e) => {
                  if (expandable && (e.key === "Enter" || e.key === " ")) {
                    e.preventDefault();
                    onOpen(c);
                  }
                }}
              >
                {img ? (
                  <span className="glyph real">
                    <img alt="" src={img} draggable={false} />
                  </span>
                ) : (
                  <span className={cleanable ? "glyph warm" : "glyph"}>
                    <Icon name={c.icon} />
                  </span>
                )}
                <div className="row-main">
                  <div className="row-title">{c.name}</div>
                  {cleanable ? (
                    <div className="row-sub">{formatBytes(c.reclaimable)} can go</div>
                  ) : (
                    <div className="share">
                      <i style={{ width: `${ratio}%` }} />
                    </div>
                  )}
                </div>
                <span className="row-size">{formatBytes(c.size)}</span>
                <button
                  type="button"
                  className="icon-btn"
                  title="Show in Finder"
                  aria-label={`Show ${c.name} in Finder`}
                  onClick={(e) => {
                    e.stopPropagation();
                    onReveal(c.path);
                  }}
                >
                  <Icon name="reveal" size={16} />
                </button>
              </div>
            );
          })}
          {kids.length > 80 && <div className="row row-sub">and {kids.length - 80} smaller items</div>}
        </div>
      )}
    </aside>
  );
}

function SpaceMap() {
  const app = useApp();
  const { maps, mapping, mapRuns, mapFailed, scanning, machine, volumes, notify } = app;
  const [nav, set] = usePatch<{ source: string; trail: SpaceNode[]; focus: string | null }>({ source: remembered.source, trail: [], focus: null });

  const sources = useMemo<Source[]>(() => [{ key: HOME_KEY, label: "Home" }, ...(volumes ?? []).map((d) => ({ key: d.mount, label: d.label, disk: d }))], [volumes]);
  const src = sources.find((s) => s.key === nav.source) ?? sources[0]!;
  const root = src.disk?.mount;
  const slot = root ?? "";
  const tree = maps[slot] ?? null;
  const busy = mapping.includes(slot);
  const home = machine?.homeDir;
  const mapLive = useLiveRun(mapRuns[slot]);
  const walking = busy ? mapLive : null;

  // After files changed the maps are dropped; measure the one on screen again unless that failed or is going on.
  useEffect(() => {
    if (!tree && !busy && !mapFailed.includes(slot) && !scanning) void app.remap(root);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberate
  }, [tree, busy, mapFailed, slot, scanning]);

  useEffect(() => set({ trail: [], focus: null }), [tree]); // eslint-disable-line react-hooks/exhaustive-deps -- deliberate

  const here = nav.trail.length > 0 ? nav.trail[nav.trail.length - 1]! : tree;
  const kids = useMemo(() => (here?.children ?? []).filter((c) => c.size > 0).sort((a, b) => b.size - a.size), [here]);
  const icons = useIcons(kids.slice(0, 80).map((c) => (c.kind === "file" ? c.path : null)));

  const bubbles = useMemo((): BubbleDatum[] => {
    const items: BubbleDatum[] = kids.slice(0, TOP_N).map((c) => ({
      id: keyOf(c), name: c.name, size: c.size, icon: c.icon, img: c.kind === "file" ? icons[c.path] : undefined,
      badge: c.reclaimable >= WORTH_NOTING ? `-${formatBytes(c.reclaimable)}` : undefined,
    }));
    const overflow = kids.slice(TOP_N);
    if (overflow.length > 0) items.push({ id: OVERFLOW, name: `${overflow.length} more`, size: overflow.reduce((a, c) => a + c.size, 0), icon: "more" });
    return items;
  }, [kids, icons]);

  const openNode = (node: SpaceNode) => {
    if (node.children?.length) set({ trail: [...nav.trail, node], focus: null });
    else set({ focus: keyOf(node) });
  };
  const bubbleClicked = (id: string) => {
    if (id === OVERFLOW) {
      const first = kids[TOP_N];
      set({ focus: first ? keyOf(first) : null });
      return;
    }
    const node = kids.find((c) => keyOf(c) === id);
    if (node) openNode(node);
  };
  const switchSource = (key: string) => {
    const dest = sources.find((s) => s.key === key);
    if (!dest || dest.key === src.key) return;
    remembered.source = key;
    set({ source: key, trail: [], focus: null });
    const slotNext = dest.disk?.mount ?? "";
    if (!maps[slotNext] && !mapping.includes(slotNext)) void app.remap(dest.disk?.mount);
  };
  const reveal = (path: string) => api.revealPath(path).catch((e: unknown) => notify(messageOf(e, "Could not open that folder"), "err"));

  useEffect(() => {
    if (nav.focus) document.getElementById(`node-${nav.focus}`)?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [nav.focus]);

  const waiting = busy || (!root && scanning);
  const headline = splitBytes(here?.size ?? 0);
  const rootName = src.disk ? src.label : tree?.name || "Home";
  const target = src.disk ? src.label : "your home folder";

  return (
    <div className="screen">
      <PageHead title="Space Map" sub="Shows what fills the disk: the bigger the bubble, the more space it holds. Click one to look inside.">
        {here && (
          <div className="head-stat">
            <span>{nav.trail.length ? "This folder" : `Mapped · ${src.label}`}</span>
            <BigNumber value={headline.value} unit={headline.unit} />
          </div>
        )}
        <button type="button" className="pbtn sm" disabled={busy} onClick={() => void app.remap(root)}>
          {busy ? <Spinner /> : <Icon name="scan" />}
          {busy ? "Mapping" : "Rescan"}
        </button>
      </PageHead>

      {sources.length > 1 && (
        <div className="source-bar rise" style={{ animationDelay: "0.03s" }}>
          <Segmented<string> label="What to map" value={src.key} onChange={switchSource} options={sources.map((s) => ({ value: s.key, label: s.label }))} />
          <SourceNote src={src} home={home} />
        </div>
      )}

      {!tree &&
        (waiting ? (
          <Waiting
            icon="bubbles"
            title={src.disk ? `Measuring ${src.label}` : "Measuring your folders"}
            text={walking?.phase ?? (src.disk ? `Walking every folder on ${target}. A full drive can take a few minutes.` : "Walking your home folder. The big ones take a moment.")}
            value={fraction(walking)}
            current={walking?.current ? tildePath(walking.current, home) : undefined}
          />
        ) : (
          <section className="glass rise">
            {mapFailed.includes(slot) ? (
              <Empty icon="warning" title={`${src.disk ? src.label : "Your folders"} could not be mapped`} text="Something interrupted it. Please try once more.">
                <button type="button" className="pbtn solid" onClick={() => void app.remap(root)}>
                  Try again
                </button>
              </Empty>
            ) : (
              <Empty icon="bubbles" title="Nothing mapped yet" text={`Measure ${target} to find out where the space goes.`}>
                <button type="button" className="pbtn solid" onClick={() => void app.remap(root)}>
                  <Icon name="scan" />Map {src.disk ? src.label : "my disk"}
                </button>
              </Empty>
            )}
          </section>
        ))}

      {tree && here && (
        <div className="map-layout">
          <section className="glass stage rise" style={{ animationDelay: "0.06s" }} aria-label="Space map">
            <div className="stage-head">
              <nav className="crumbs" aria-label="Folder path">
                {[tree, ...nav.trail].map((n, depth, all) => (
                  <span key={depth} style={{ display: "contents" }}>
                    {depth > 0 && <span className="sep">/</span>}
                    <button type="button" aria-current={depth === all.length - 1} onClick={() => set({ trail: nav.trail.slice(0, depth), focus: null })}>
                      {depth === 0 ? rootName : n.name}
                    </button>
                  </span>
                ))}
              </nav>
              <span className="fill" />
              {nav.trail.length > 0 && (
                <button type="button" className="pbtn sm quiet" onClick={() => set({ trail: nav.trail.slice(0, -1) })}>
                  <Icon name="arrow" size={16} style={{ transform: "rotate(180deg)" }} /> Up
                </button>
              )}
            </div>
            <div className="stage-body">
              {bubbles.length > 0 ? (
                <Bubbles items={bubbles} onSelect={bubbleClicked} selectedId={nav.focus ?? undefined} />
              ) : (
                <Empty icon="folder" title="Nothing big in here" text="The folder is empty or holds only tiny files." />
              )}
            </div>
            <div className="stage-foot">
              <i className="key" />
              <span>A badge marks something you could clean</span>
              <span className="fill" />
              {busy && (
                <>
                  <Spinner size={14} />
                  <span>Updating</span>
                </>
              )}
            </div>
          </section>
          <SidePanel
            folder={here}
            title={here === tree ? rootName : here.name}
            kids={kids}
            focus={nav.focus}
            icons={icons}
            home={home}
            onOpen={openNode}
            onReveal={(p) => void reveal(p)}
            onReview={() => app.navigate("cleanup")}
          />
        </div>
      )}
    </div>
  );
}

export default SpaceMap;
