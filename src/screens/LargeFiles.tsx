import { memo, useCallback, useEffect, useMemo, useRef } from "react";
import { api } from "../api";
import { AskBar, PickedBar } from "../components/ActionBar";
import { Icon } from "../components/Icon";
import { BigNumber, Empty, Meter, PageHead, Segmented, Spinner, Waiting, fraction } from "../components/ui";
import { isPartial, trashSummary } from "../lib/cleanSummary";
import { isConfirmError, isScanExpired } from "../lib/confirm";
import { dirOf, formatBytes, countOf, hiddenNote, messageOf, splitBytes, sumOf, tildePath, timeAgo, wasCancelled } from "../lib/format";
import { useCachedScan, useChoice, useIcons, usePatch } from "../lib/hooks";
import { createSingleFlight } from "../lib/singleFlight";
import { useApp, useLive, usePlatform, volumeOf } from "../store";
import type { DiskInfo, IconName, LargeFile } from "../types";
import "./lists.css";

type Group = "all" | "video" | "archive" | "disk-image" | "installer" | "other";
type Order = "size" | "oldest";

const GROUPS: [Group, string][] = [
  ["all", "All"], ["video", "Videos"], ["archive", "Archives"], ["disk-image", "Disk images"], ["installer", "Installers"], ["other", "Other"],
];
const GLYPH: Record<LargeFile["kind"], IconName> = {
  video: "video", archive: "layers", "disk-image": "chip", installer: "down", audio: "music", photo: "photo", document: "doc", other: "file",
};
const NAMED_KINDS: string[] = ["video", "archive", "disk-image", "installer"];
const groupOf = (f: LargeFile): Group => (NAMED_KINDS.includes(f.kind) ? (f.kind as Group) : "other");
const touchedAt = (f: LargeFile) => Math.max(f.accessed || 0, f.modified || 0);
const MIB = 1024 * 1024;

interface FileRowProps {
  file: LargeFile;
  on: boolean;
  locked: boolean;
  home?: string;
  volumes: DiskInfo[] | null;
  icon?: string | undefined;
  onFlip: (path: string) => void;
  onReveal: (path: string) => void;
}

const FileRow = memo(function FileRow({ file, on, locked, home, volumes, icon, onFlip, onReveal }: FileRowProps) {
  const { fileManager } = usePlatform();
  const vol = volumeOf(file.path, home, volumes);
  const wasOpened = file.accessed > 0 && file.accessed >= file.modified;
  return (
    <label className={on ? "row is-on" : "row"}>
      <input type="checkbox" className="tick" checked={on} disabled={locked} onChange={() => onFlip(file.path)} aria-label={file.name} />
      {icon ? (
        <span className="glyph real">
          <img src={icon} alt="" draggable={false} />
        </span>
      ) : (
        <span className={`glyph kind-${file.kind}`}>
          <Icon name={GLYPH[file.kind]} />
        </span>
      )}
      <div className="row-main">
        <div className="row-title" title={file.name}>
          {file.name}
        </div>
        <div className="row-sub mono" title={file.path}>
          {vol ? (
            <>
              <span className="volume-tag">{vol.name}</span>
              {vol.dir}
            </>
          ) : (
            tildePath(dirOf(file.path), home)
          )}
        </div>
      </div>
      <span className="row-meta">
        {wasOpened ? "last opened" : "changed"} {timeAgo(touchedAt(file))}
      </span>
      <span className="row-size">{formatBytes(file.size)}</span>
      <button
        type="button"
        className="icon-btn"
        title={`Show in ${fileManager}`}
        aria-label={`Show ${file.name} in ${fileManager}`}
        onClick={(e) => {
          e.preventDefault();
          onReveal(file.path);
        }}
      >
        <Icon name="reveal" size={16} />
      </button>
    </label>
  );
});

function LargeFiles() {
  const app = useApp();
  const bin = app.platform.trash;
  const { prefs, machine, volumes, notify } = app;
  const scanLive = useLive("large");
  const threshold = prefs ? prefs.largeFileMinMb * MIB : null;
  const scan = useCachedScan(threshold === null ? null : `large:${threshold}`, (runId) => api.scanLargeFiles(runId, threshold ?? 0), (e) =>
    notify(messageOf(e, "Large file search failed"), "err"),
  );
  const files = scan.data?.files ?? null;
  const hiddenLine = hiddenNote(scan.data?.hidden ?? 0, "file", "files");
  const hasFiles = (files?.length ?? 0) > 0;
  const [ui, set] = usePatch<{ group: Group; order: Order; asking: boolean; moving: boolean }>({ group: "all", order: "size", asking: false, moving: false });
  const picks = useChoice();

  // New data or a different filter changes what a pending confirmation would cover.
  useEffect(() => set({ asking: false }), [files, ui.group]); // eslint-disable-line react-hooks/exhaustive-deps -- deliberate

  const home = machine?.homeDir;
  const multiVolume = (volumes?.length ?? 0) > 1;

  const tally = useMemo(() => {
    const t: Record<Group, number> = { all: 0, video: 0, archive: 0, "disk-image": 0, installer: 0, other: 0 };
    (files ?? []).forEach((f) => {
      t.all += 1;
      t[groupOf(f)] += 1;
    });
    return t;
  }, [files]);

  const visible = useMemo(() => {
    const pool = (files ?? []).filter((f) => ui.group === "all" || groupOf(f) === ui.group);
    return pool.sort(ui.order === "size" ? (a, b) => b.size - a.size : (a, b) => touchedAt(a) - touchedAt(b));
  }, [files, ui.group, ui.order]);

  const icons = useIcons(visible.map((f) => f.path));
  // Only what is on screen counts: a pick hidden by the filter is never sent.
  const picked = visible.filter((f) => picks.has(f.path));
  const pickedBytes = sumOf(picked.map((f) => f.size));
  const allVisiblePicked = visible.length > 0 && visible.every((f) => picks.has(f.path));
  const grandTotal = splitBytes(sumOf((files ?? []).map((f) => f.size)));

  const reveal = useCallback((path: string) => void api.revealPath(path).catch((e: unknown) => notify(messageOf(e, "Could not show that file"), "err")), [notify]);

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
      picks.reset();
      void scan.rerun();
      return;
    }
    set({ asking: false, moving: true });
    const paths = picked.map((f) => f.path);
    try {
      const res = await api.trashPaths(scanId, paths);
      const out = trashSummary(res, "file", "files", bin);
      notify(out.text, out.tone);
      if (isPartial(res) || res.cancelled) {
        // something stayed or the run was stopped: only a new look shows what is really left
        app.afterChange();
        void scan.rerun();
      } else {
        scan.replace({ hidden: scan.data?.hidden ?? 0, files: (files ?? []).filter((f) => !paths.includes(f.path)) });
        app.afterChange();
      }
      picks.reset();
    } catch (e) {
      if (isScanExpired(e)) {
        notify("Results were out of date. Review and choose again", "err");
        app.afterChange();
        picks.reset();
        void scan.rerun();
      } else if (isConfirmError(e)) {
        notify(`The confirmation ran out. Choose Move to ${bin} again`, "err");
      } else if (wasCancelled(e)) {
        notify("Moving stopped");
        app.afterChange();
        void scan.rerun();
      } else {
        notify(messageOf(e, `Could not move files to ${bin}`), "err");
      }
    } finally {
      set({ moving: false });
    }
  };
  const press = () => (prefs?.confirmBeforeClean === false ? void moveToTrash() : set({ asking: true }));

  const limitText = prefs ? formatBytes(prefs.largeFileMinMb * MIB) : "...";
  const plural = picked.length === 1 ? "file" : "files";

  return (
    <div className="screen">
      <PageHead title="Large Files" sub={`Files above ${limitText} you might have forgotten${multiVolume ? ", across all your drives" : ""}. Whatever you remove goes to the ${bin}.`}>
        {files && hasFiles && (
          <div className="head-stat">
            <span>{countOf(files.length, "file", "files")}</span>
            <BigNumber value={grandTotal.value} unit={grandTotal.unit} />
          </div>
        )}
        <button type="button" className="pbtn sm" disabled={scan.busy || ui.moving || ui.asking || threshold === null} onClick={() => void scan.rerun()}>
          {scan.busy ? <Spinner /> : <Icon name="scan" />}
          {scan.busy ? "Searching" : "Rescan"}
        </button>
      </PageHead>

      {hiddenLine && <p className="head-note rise">{hiddenLine}</p>}

      {!files &&
        (scan.broken || scan.cancelled ? (
          <section className="glass rise">
            <Empty icon="warning" title="The search stopped early" text="Something interrupted it. Please try once more.">
              <button type="button" className="pbtn solid" onClick={() => void scan.rerun()}>
                Try again
              </button>
            </Empty>
          </section>
        ) : (
          <Waiting
            icon="file"
            title="Looking for big files"
            text={scanLive?.phase ?? (multiVolume ? "Going through your home folder and the other drives." : "Going through Downloads, Desktop, Documents, Movies and more.")}
            value={fraction(scanLive)}
            current={scanLive?.current ? tildePath(scanLive.current, home) : undefined}
          />
        ))}

      {files?.length === 0 && (
        <section className="glass rise">
          {hiddenLine ? (
            <Empty icon="check" title="Nothing you can move here" text={`${countOf(scan.data?.hidden ?? 0, "file", "files")} over your size limit ${(scan.data?.hidden ?? 0) === 1 ? "is" : "are"} protected by safety rules.`} />
          ) : (
            <Empty icon="check" title="No large files" text="Nothing is over your size limit. Settings lets you lower it." />
          )}
        </section>
      )}

      {files && hasFiles && (
        <>
          <div className="filters rise" style={{ animationDelay: "0.04s" }}>
            {GROUPS.map(([id, label]) => (
              <button key={id} type="button" className="chip-filter" aria-pressed={ui.group === id} disabled={id !== "all" && tally[id] === 0} onClick={() => set({ group: id })}>
                {label}
                <i>{tally[id]}</i>
              </button>
            ))}
            <span className="fill" />
            <Segmented<Order>
              label="Sort files"
              value={ui.order}
              onChange={(order) => set({ order })}
              options={[
                { value: "size", label: "Size" },
                { value: "oldest", label: "Oldest" },
              ]}
            />
          </div>

          <section className="glass list-card rise" style={{ animationDelay: "0.08s" }}>
            {scan.busy && (
              <div className="strip">
                <Spinner />
                <span>{scanLive?.phase ?? "Searching again"}</span>
                <Meter value={fraction(scanLive)} />
              </div>
            )}
            <div className="list-head">
              <h2>{GROUPS.find(([id]) => id === ui.group)?.[1]}</h2>
              <span className="count">
                {visible.length} {"·"} {formatBytes(sumOf(visible.map((f) => f.size)))}
              </span>
              <span className="fill" />
              <button
                type="button"
                className="text-link"
                disabled={visible.length === 0 || ui.moving}
                onClick={() => (allVisiblePicked ? picks.removeAll(visible.map((f) => f.path)) : picks.addAll(visible.map((f) => f.path)))}
              >
                {allVisiblePicked ? "Select none" : "Select all"}
              </button>
            </div>
            {visible.length === 0 ? (
              <Empty icon="search" title="This group is empty" />
            ) : (
              <div className="rows">
                {visible.map((f) => (
                  <FileRow key={f.path} file={f} on={picks.has(f.path)} locked={ui.moving} home={home} volumes={volumes} icon={icons[f.path]} onFlip={picks.flip} onReveal={reveal} />
                ))}
              </div>
            )}
          </section>

          {picked.length > 0 && !ui.asking && (
            <PickedBar bytes={pickedBytes} caption={`${picked.length} ${plural} · goes to ${bin}, restorable`}>
              {ui.moving ? (
                <button type="button" className="pbtn quiet" onClick={() => app.abort(["trash"])}>
                  Stop
                </button>
              ) : (
                <button type="button" className="pbtn quiet" onClick={() => picks.reset()}>
                  Clear
                </button>
              )}
              <button type="button" className="pbtn solid" disabled={ui.moving || scan.busy} onClick={press}>
                {ui.moving ? <Spinner light size={16} /> : <Icon name="trash" size={16} />}
                {ui.moving ? "Moving" : `Move to ${bin}`}
              </button>
            </PickedBar>
          )}
          {picked.length > 0 && ui.asking && (
            <AskBar
              icon="trash"
              title={`Move ${picked.length} ${plural} (${formatBytes(pickedBytes)}) to ${bin}?`}
              note={`They stay restorable until the ${bin} is emptied.`}
              yes={`Move to ${bin}`}
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

export default LargeFiles;
