import { useEffect, useMemo, useRef } from "react";
import { api } from "../api";
import { AskBar, PickedBar } from "../components/ActionBar";
import { Icon } from "../components/Icon";
import { BigNumber, Empty, Meter, PageHead, Spinner, Waiting, fraction } from "../components/ui";
import { isPartial, trashSummary } from "../lib/cleanSummary";
import { isConfirmError, isScanExpired } from "../lib/confirm";
import { dirOf, formatBytes, formatDate, countOf, hiddenNote, leafName, messageOf, splitBytes, sumOf, tildePath, wasCancelled } from "../lib/format";
import { useCachedScan, useChoice, useIcons, usePatch } from "../lib/hooks";
import { createSingleFlight } from "../lib/singleFlight";
import { useApp, useLive, volumeOf } from "../store";
import type { DuplicateGroup, DuplicateScan, IconName } from "../types";
import "./lists.css";

const BATCH = 30;
const BY_EXTENSION: [RegExp, IconName][] = [
  [/\.(mp4|mov|mkv|avi|webm|m4v)$/i, "video"],
  [/\.(jpe?g|png|gif|heic|webp|tiff?|bmp|raw|dng|psd)$/i, "photo"],
  [/\.(mp3|wav|flac|aac|m4a|ogg|aif)$/i, "music"],
  [/\.(zip|rar|7z|tar|gz|tgz|bz2|xz)$/i, "layers"],
  [/\.(pdf|docx?|xlsx?|pptx?|txt|rtf|pages|key|numbers|csv)$/i, "doc"],
  [/\.(dmg|iso|img|pkg)$/i, "down"],
  [/\.(js|ts|py|json|html|css|java|c|cpp|go|rs|swift)$/i, "code"],
];
const glyphFor = (name: string): IconName => BY_EXTENSION.find(([re]) => re.test(name))?.[1] ?? "file";
const latest = (g: DuplicateGroup) => [...g.files].sort((a, b) => b.modified - a.modified)[0]!.path;
/** The copy that stays: the chosen one if it is still in the group, otherwise the newest. */
const keeperFor = (g: DuplicateGroup, keeps: Record<string, string>) => {
  const chosen = keeps[g.hash];
  return chosen && g.files.some((f) => f.path === chosen) ? chosen : latest(g);
};
const spare = (g: DuplicateGroup) => g.size * (g.files.length - 1);

const findGroups = async (runId: string): Promise<DuplicateScan> => {
  const found = await api.scanDuplicates(runId);
  return { ...found, groups: found.groups.filter((g) => g.files.length > 1).sort((a, b) => spare(b) - spare(a)) };
};

function GroupCard({ group, index, keeper, skipped, locked, home, icon, onSkip, onKeep, onReveal }: {
  group: DuplicateGroup; index: number; keeper: string; skipped: boolean; locked: boolean; home?: string; icon?: string | undefined;
  onSkip: () => void; onKeep: (path: string) => void; onReveal: (path: string) => void;
}) {
  const volumes = useApp().volumes;
  const first = group.files[0]!.path;
  const name = leafName(first);
  const newest = latest(group);
  return (
    <section className={skipped ? "glass dupe-group rise is-skipped" : "glass dupe-group rise"} style={{ animationDelay: `${Math.min(index, 8) * 0.04}s` }}>
      <div className="dupe-head">
        <input type="checkbox" className="tick" checked={!skipped} disabled={locked} onChange={onSkip} aria-label={`Include ${name}`} />
        {icon ? (
          <span className="glyph real">
            <img src={icon} alt="" draggable={false} />
          </span>
        ) : (
          <span className="glyph">
            <Icon name={glyphFor(name)} />
          </span>
        )}
        <div className="row-main">
          <div className="row-title" title={name}>
            {name}
          </div>
          <div className="row-sub">
            {countOf(group.files.length, "copy", "copies")} {"·"} {formatBytes(group.size)} each
          </div>
        </div>
        <div className="wasted">
          <b>{formatBytes(spare(group))}</b>
          <span>wasted</span>
        </div>
      </div>
      <div className="rows copies" role="radiogroup" aria-label={`Which copy of ${name} to keep`}>
        {group.files.map((f) => {
          const isKeeper = f.path === keeper;
          const vol = volumeOf(f.path, home, volumes);
          return (
            <label key={f.path} className={isKeeper ? "row is-kept" : "row"}>
              <input type="radio" className="radio" name={`keep-${group.hash}`} checked={isKeeper} disabled={locked} onChange={() => onKeep(f.path)} aria-label={`Keep ${leafName(f.path)} in ${vol ? vol.name + vol.dir : tildePath(dirOf(f.path), home)}`} />
              <div className="row-main">
                <div className="row-title" title={f.path}>
                  {vol ? (
                    <>
                      <span className="volume-tag">{vol.name}</span>
                      {vol.dir}
                    </>
                  ) : (
                    tildePath(dirOf(f.path), home)
                  )}
                  {` / ${leafName(f.path)}`}
                </div>
                <div className="row-sub">
                  {`Modified ${formatDate(f.modified)}`}
                  {f.path === newest ? " · newest" : ""}
                </div>
              </div>
              <span className={isKeeper ? "fate keep" : "fate trash"}>{isKeeper ? "Keep" : skipped ? "Leave" : "To Trash"}</span>
              <button
                type="button"
                className="icon-btn"
                title="Show in Finder"
                aria-label="Show in Finder"
                onClick={(e) => {
                  e.preventDefault();
                  onReveal(f.path);
                }}
              >
                <Icon name="reveal" size={16} />
              </button>
            </label>
          );
        })}
      </div>
    </section>
  );
}

function Duplicates() {
  const app = useApp();
  const { machine, prefs, notify } = app;
  const scanLive = useLive("dupes");
  const scan = useCachedScan("duplicates", findGroups, (e) => notify(messageOf(e, "Comparison failed"), "err"));
  const groups = scan.data?.groups ?? null;
  const hiddenLine = hiddenNote(scan.data?.hidden ?? 0, "copy", "copies");
  const [ui, set] = usePatch<{ keeps: Record<string, string>; shown: number; asking: boolean; moving: boolean }>({ keeps: {}, shown: BATCH, asking: false, moving: false });
  const skipped = useChoice();
  const home = machine?.homeDir;
  const keeperOf = (g: DuplicateGroup) => keeperFor(g, ui.keeps);
  const rendered = useMemo(() => (groups ?? []).slice(0, ui.shown), [groups, ui.shown]);

  // New scan data means old keeper picks and skips may point at files that are gone.
  useEffect(() => {
    set({ keeps: {}, asking: false });
    skipped.reset();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberate
  }, [groups]);

  const doomed = useMemo(
    () =>
      rendered
        .filter((g) => !skipped.has(g.hash))
        .flatMap((g) => {
          const keeper = keeperFor(g, ui.keeps);
          const others = g.files.filter((f) => f.path !== keeper);
          // never offer a group whose every copy would go
          return others.length < g.files.length ? others.map((f) => ({ path: f.path, size: g.size })) : [];
        }),
    [rendered, skipped, ui.keeps],
  );
  const doomedBytes = sumOf(doomed.map((d) => d.size));
  const wastedTotal = splitBytes(sumOf((groups ?? []).map(spare)));
  const icons = useIcons((groups ?? []).slice(0, ui.shown).map((g) => g.files[0]?.path));
  const count = doomed.length;
  const hasGroups = (groups?.length ?? 0) > 0;
  const copies = count === 1 ? "copy" : "copies";

  const reveal = (path: string) => api.revealPath(path).catch((e: unknown) => notify(messageOf(e, "Could not show that file"), "err"));

  // a double click can reach here twice before the button is disabled; only the first may ask Go for a token
  const once = useRef(createSingleFlight()).current;
  const moveToTrash = () => once(moveNow);
  const moveNow = async () => {
    const scanId = scan.runId;
    if (!scanId) return;
    if (scan.expired()) {
      // the list sat open past its time limit: look again instead of acting on it
      set({ asking: false });
      notify("Results were out of date. Review and choose again", "err");
      app.afterChange();
      set({ keeps: {} });
      skipped.reset();
      void scan.rerun();
      return;
    }
    set({ asking: false, moving: true });
    const paths = doomed.map((d) => d.path);
    try {
      const res = await api.trashPaths(scanId, paths);
      const out = trashSummary(res, "copy", "copies");
      notify(out.text, out.tone);
      if (isPartial(res) || res.cancelled) {
        // something stayed or the run was stopped: only a new look shows what is really left
        app.afterChange();
        void scan.rerun();
      } else {
        scan.replace({ hidden: scan.data?.hidden ?? 0, groups: (groups ?? []).map((g) => ({ ...g, files: g.files.filter((f) => !paths.includes(f.path)) })).filter((g) => g.files.length > 1) });
        app.afterChange();
      }
      set({ keeps: {} });
      skipped.reset();
    } catch (e) {
      if (isScanExpired(e)) {
        notify("Results were out of date. Review and choose again", "err");
        app.afterChange();
        set({ keeps: {} });
        skipped.reset();
        void scan.rerun();
      } else if (isConfirmError(e)) {
        notify("The confirmation ran out. Choose Move to Trash again", "err");
      } else if (wasCancelled(e)) {
        notify("Moving stopped");
        app.afterChange();
        void scan.rerun();
      } else {
        notify(messageOf(e, "Could not move copies to Trash"), "err");
      }
    } finally {
      set({ moving: false });
    }
  };

  return (
    <div className="screen">
      <PageHead title="Duplicates" sub="Files with identical contents, whatever they are called. The newest copy stays; the others go to the Trash.">
        {hasGroups && (
          <div className="head-stat">
            <span>Wasted</span>
            <BigNumber value={wastedTotal.value} unit={wastedTotal.unit} />
          </div>
        )}
        <button type="button" className="pbtn sm" disabled={scan.busy || ui.moving || ui.asking} onClick={() => void scan.rerun()}>
          {scan.busy ? <Spinner /> : <Icon name="scan" />}
          {scan.busy ? "Comparing" : "Rescan"}
        </button>
      </PageHead>

      {hiddenLine && <p className="head-note rise">{hiddenLine}</p>}

      {!groups &&
        (scan.broken || scan.cancelled ? (
          <section className="glass rise">
            <Empty icon="warning" title="The comparison stopped early" text="Something interrupted it. Please try once more.">
              <button type="button" className="pbtn solid" onClick={() => void scan.rerun()}>
                Try again
              </button>
            </Empty>
          </section>
        ) : (
          <Waiting
            icon="dupes"
            title="Comparing your files"
            text={`${scanLive?.phase ? `${scanLive.phase}. ` : ""}Contents are checked byte by byte, which may take a minute or two. You can browse other screens meanwhile.`}
            value={fraction(scanLive)}
            current={scanLive?.current ? tildePath(scanLive.current, home) : undefined}
          />
        ))}

      {groups?.length === 0 && (
        <section className="glass rise">
          {hiddenLine ? (
            <Empty icon="check" title="Nothing you can move here" text={`${countOf(scan.data?.hidden ?? 0, "copy", "copies")} ${(scan.data?.hidden ?? 0) === 1 ? "is" : "are"} protected by safety rules.`} />
          ) : (
            <Empty icon="check" title="No duplicates" text="Each file here is unique. Nicely tidy." />
          )}
        </section>
      )}

      {groups && hasGroups && (
        <>
          {scan.busy && (
            <section className="glass">
              <div className="strip">
                <Spinner />
                <span>{scanLive?.phase ?? "Comparing again"}</span>
                <Meter value={fraction(scanLive)} />
              </div>
            </section>
          )}
          <div className="group-stack">
            {rendered.map((g, i) => (
              <GroupCard
                key={g.hash}
                group={g}
                index={i}
                keeper={keeperOf(g)}
                skipped={skipped.has(g.hash)}
                locked={ui.moving}
                home={home}
                icon={icons[g.files[0]!.path]}
                onSkip={() => skipped.flip(g.hash)}
                onKeep={(path) => set({ keeps: { ...ui.keeps, [g.hash]: path } })}
                onReveal={(p) => void reveal(p)}
              />
            ))}
            {groups.length > ui.shown && (
              <button type="button" className="pbtn more" onClick={() => set({ shown: ui.shown + BATCH })}>
                Show {Math.min(BATCH, groups.length - ui.shown)} more of {groups.length - ui.shown}
              </button>
            )}
          </div>

          {count > 0 && !ui.asking && (
            <PickedBar bytes={doomedBytes} caption={`${rendered.filter((g) => !skipped.has(g.hash)).length} of ${countOf(rendered.length, "group", "groups")} · one copy of each stays`}>
              {ui.moving && (
                <button type="button" className="pbtn quiet" onClick={() => app.abort(["trash"])}>
                  Stop
                </button>
              )}
              <button type="button" className="pbtn solid" disabled={ui.moving || scan.busy} onClick={() => (prefs?.confirmBeforeClean === false ? void moveToTrash() : set({ asking: true }))}>
                {ui.moving ? <Spinner light size={16} /> : <Icon name="trash" size={16} />}
                {ui.moving ? "Moving" : `Move ${count} ${copies} to Trash`}
              </button>
            </PickedBar>
          )}
          {count > 0 && ui.asking && (
            <AskBar
              icon="trash"
              title={`Move ${count} ${copies} (${formatBytes(doomedBytes)}) to Trash?`}
              note="A copy of every file stays where it is, and the rest can be restored from the Trash."
              yes="Move to Trash"
              yesDisabled={ui.moving}
              onNo={() => set({ asking: false })}
              onYes={() => void moveToTrash()}
            />
          )}
        </>
      )}
    </div>
  );
}

export default Duplicates;
