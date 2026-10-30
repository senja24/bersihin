import { useEffect, useMemo, useRef } from "react";
import { AskBar, PickedBar } from "../components/ActionBar";
import { Icon } from "../components/Icon";
import { Capsule, Empty, PageHead, Spinner, Waiting, fraction } from "../components/ui";
import { needsAsk } from "../lib/armedClean";
import { cleanSummary } from "../lib/cleanSummary";
import { formatBytes, isIrreversible, sumOf, tildePath } from "../lib/format";
import { useChoice, usePatch } from "../lib/hooks";
import { useApp, useLive } from "../store";
import type { JunkCategory } from "../types";
import "./lists.css";

const cannotUndo = isIrreversible;
const byTrust = (a: JunkCategory, b: JunkCategory) => Number(b.safe) - Number(a.safe) || b.size - a.size;

function JunkRow({ cat, on, locked, home, onFlip }: { cat: JunkCategory; on: boolean; locked: boolean; home?: string; onFlip: () => void }) {
  const nothing = cat.size <= 0;
  const permanent = cannotUndo(cat);
  const tint = permanent ? "glyph warm" : cat.safe ? "glyph" : "glyph muted";
  return (
    <label className={["row", on && "is-on", nothing && "is-empty"].filter(Boolean).join(" ")}>
      <input type="checkbox" className="tick" checked={on} disabled={nothing || locked} onChange={onFlip} aria-label={cat.name} />
      <span className={tint}>
        <Icon name={cat.icon} />
      </span>
      <div className="row-main">
        <div className="row-title">{cat.name}</div>
        <div className="row-desc">{cat.description}</div>
        <div className="row-sub mono" title={cat.locations.join("\n")}>
          {cat.locations.map((l) => tildePath(l, home)).join("  ·  ")}
        </div>
      </div>
      <div className="status-group">
        {permanent && (
          <span className="status risk">
            <Icon name="warning" size={12} />
            Permanent
          </span>
        )}
        {nothing ? null : <span className={cat.safe ? "status safe" : "status review"}>{cat.safe ? "Safe" : "Review"}</span>}
      </div>
      <span className="row-size">{nothing ? "Clean" : formatBytes(cat.size)}</span>
    </label>
  );
}

function Cleanup() {
  const app = useApp();
  const { junk, scanning, cleaning, machine, prefs } = app;
  const scanLive = useLive("junk");
  const cleanLive = useLive("clean");
  const [flow, set] = usePatch({ asking: false, finished: false });
  const picks = useChoice();
  const triedOnce = useRef(false);

  // Arriving without results starts a scan, once; a failure leaves the retry button.
  useEffect(() => {
    if (junk || scanning || triedOnce.current) return;
    triedOnce.current = true;
    void app.rescan();
  }, [junk, scanning, app]);

  // Every new result set starts with the safe categories ticked.
  useEffect(() => {
    picks.reset((junk ?? []).filter((c) => c.safe && c.size > 0).map((c) => c.id));
    set({ asking: false });
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberate
  }, [junk]);

  const ordered = useMemo(() => [...(junk ?? [])].sort(byTrust), [junk]);
  const removable = ordered.filter((c) => c.size > 0);
  const targets = removable.filter((c) => picks.has(c.id));
  const bytes = sumOf(targets.map((c) => c.size));
  const everythingOn = removable.length > 0 && targets.length === removable.length;
  const includesPermanent = targets.some(cannotUndo);

  const run = async () => {
    set({ asking: false });
    const res = await app.sweep(targets.map((c) => c.id));
    if (!res) return;
    set({ finished: true });
    app.notify(cleanSummary(res));
  };
  const press = () => {
    if (targets.length === 0) return;
    // permanent categories always ask, whatever the preference says
    if (needsAsk(prefs?.confirmBeforeClean, includesPermanent)) set({ asking: true });
    else void run();
  };

  let body;
  if (!junk) {
    body = scanning ? (
      <Waiting
        icon="broom"
        title="Looking for junk"
        text={scanLive?.phase ?? "Checking caches, temp folders and logs"}
        value={fraction(scanLive)}
        current={scanLive?.current ? tildePath(scanLive.current, machine?.homeDir) : undefined}
      />
    ) : (
      <section className="glass rise">
        <Empty icon="scan" title="No scan yet" text="Start a quick scan to see what can go.">
          <button type="button" className="pbtn solid" onClick={() => void app.rescan()}>
            <Icon name="scan" />Scan now
          </button>
        </Empty>
      </section>
    );
  } else {
    body = (
      <section className="glass list-card rise" style={{ animationDelay: "0.06s" }}>
        <div className="list-head">
          <h2>What we found</h2>
          <span className="count">
            {removable.length} of {ordered.length} have something to clean
          </span>
          <span className="fill" />
          <button
            type="button"
            className="text-link"
            disabled={removable.length === 0 || cleaning}
            onClick={() => (everythingOn ? picks.reset() : picks.reset(removable.map((c) => c.id)))}
          >
            {everythingOn ? "Select none" : "Select all"}
          </button>
        </div>
        {removable.length === 0 && !scanning ? (
          <Empty icon="check" title="All clean" text="There is nothing to remove at the moment. Look again in a few days." />
        ) : (
          <div className="rows">
            {ordered.map((cat) => (
              <JunkRow
                key={cat.id}
                cat={cat}
                on={picks.has(cat.id) && cat.size > 0}
                locked={cleaning}
                home={machine?.homeDir}
                onFlip={() => {
                  set({ finished: false });
                  picks.flip(cat.id);
                }}
              />
            ))}
          </div>
        )}
      </section>
    );
  }

  const itemTotal = sumOf(targets.map((c) => c.count));

  return (
    <div className="screen">
      <PageHead title="Cleanup" sub="Caches, temp files and logs that your apps rebuild by themselves. The safe ones are already ticked.">
        <button type="button" className="pbtn sm" disabled={scanning || cleaning} onClick={() => void app.rescan()}>
          {scanning ? <Spinner /> : <Icon name="scan" />}
          {scanning ? "Scanning" : "Rescan"}
        </button>
      </PageHead>
      {body}
      {junk && removable.length > 0 && !flow.asking && (
        <PickedBar
          bytes={bytes}
          caption={cleaning ? (cleanLive?.phase ?? "Cleaning") : `${targets.length} ${targets.length === 1 ? "category" : "categories"} · ${itemTotal === 1 ? "1 item" : `${itemTotal.toLocaleString("en-US")} items`}`}
        >
          <Capsule
            label={cleaning ? "Cleaning" : flow.finished && targets.length === 0 ? "Done" : "Clean"}
            sub={formatBytes(bytes)}
            busy={cleaning}
            done={flow.finished && targets.length === 0}
            disabled={targets.length === 0 || scanning}
            onClick={press}
          />
        </PickedBar>
      )}
      {junk && removable.length > 0 && flow.asking && (
        <AskBar
          icon={includesPermanent ? "warning" : "broom"}
          warm={includesPermanent}
          title={`Delete ${formatBytes(bytes)} of caches and junk?`}
          note={includesPermanent ? "These are deleted for good and cannot be undone." : "Apps recreate these files whenever they need them."}
          alarming={includesPermanent}
          yes="Delete"
          yesDisabled={scanning || cleaning}
          danger={includesPermanent}
          onNo={() => set({ asking: false })}
          onYes={() => void run()}
        />
      )}
    </div>
  );
}

export default Cleanup;
