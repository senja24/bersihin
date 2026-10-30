import { api } from "../api";
import { Icon } from "../components/Icon";
import { Empty, PageHead, Spinner, Switch } from "../components/ui";
import { messageOf } from "../lib/format";
import { useCachedScan, useIcons, usePatch } from "../lib/hooks";
import { changeStartup } from "../lib/startupChange";
import { useApp } from "../store";
import type { StartupItem } from "../types";
import "../styles/tiles.css";
import "./system.css";

type Impact = StartupItem["impact"];

const TIERS: { tier: Impact; heading: string; blurb: string }[] = [
  { tier: "high", heading: "High impact", blurb: "These slow your start the most" },
  { tier: "medium", heading: "Medium impact", blurb: "Noticeable, but lighter" },
  { tier: "low", heading: "Low impact", blurb: "Barely felt at login" },
];
const PASSWORD_NOTE = "macOS will ask for your password";

/** The .app bundle that a login item's command runs from, when there is one. */
const bundleOf = (command: string) => /^(\/.+?\.app)(?=\/|\s|$)/.exec(command.trim())?.[1];

function LoginRow({ item, pending, icon, onSet }: { item: StartupItem; pending: boolean; icon: string | undefined; onSet: (on: boolean) => void }) {
  const needsPassword = item.canToggle && item.needsAdmin;
  const cls = ["row", !item.enabled && "is-off", pending && "is-busy"].filter(Boolean).join(" ");
  return (
    <div className={cls}>
      {icon ? (
        <span className="app-icon">
          <img src={icon} alt="" draggable={false} />
        </span>
      ) : (
        <span className={`glyph impact-${item.impact}`}>
          <Icon name="power" />
        </span>
      )}
      <div className="row-main">
        <div className="title-line">
          <span className="row-title">{item.name}</span>
          {needsPassword && (
            <span className="admin-tag" title={PASSWORD_NOTE}>
              <Icon name="shield" size={11} />
              Admin
            </span>
          )}
        </div>
        <div className="row-sub mono" title={item.command}>
          {item.command || "No command recorded"}
        </div>
        {needsPassword && <div className={pending ? "row-sub ask is-on" : "row-sub ask"}>{pending ? "Waiting for your permission..." : PASSWORD_NOTE}</div>}
      </div>
      <span className="where">{item.location}</span>
      {!item.canToggle ? (
        <span className="locked" title="Controlled by your system or an administrator policy">
          <Icon name="lock" size={14} />
          Managed by the system
        </span>
      ) : pending ? (
        <span className="pending" role="status" aria-label={`Changing ${item.name}`}>
          <Spinner />
        </span>
      ) : (
        <Switch checked={item.enabled} label={`Start ${item.name} at login${needsPassword ? ` (${PASSWORD_NOTE})` : ""}`} onChange={onSet} />
      )}
    </div>
  );
}

function Speed() {
  const { notify } = useApp();
  const scan = useCachedScan("startup", () => api.startupItems(), (e) => notify(messageOf(e, "Could not read login items"), "err"));
  const [work, set] = usePatch<{ busyId: string | null }>({ busyId: null });
  const all = scan.data ?? [];
  const icons = useIcons(all.map((i) => bundleOf(i.command)));

  const change = async (item: StartupItem, on: boolean) => {
    if (work.busyId) return;
    set({ busyId: item.id });
    try {
      await changeStartup({ set: api.setStartupItem, show: scan.replace, reload: scan.rerun, notify }, item, on);
    } finally {
      set({ busyId: null });
    }
  };

  const running = all.filter((i) => i.enabled);
  const fraction = all.length ? running.length / all.length : 0;
  const heavyRunning = running.filter((i) => i.impact === "high").length;
  const managed = all.filter((i) => !i.canToggle).length;
  const passwordGated = all.filter((i) => i.canToggle && i.needsAdmin).length;

  return (
    <div className="screen">
      <PageHead title="Speed" sub="Choose which apps open when you log in. The fewer there are, the quicker and calmer the start.">
        <button type="button" className="pbtn sm" disabled={scan.busy} onClick={() => void scan.rerun()}>
          {scan.busy ? <Spinner /> : <Icon name="history" size={16} />}
          Refresh
        </button>
      </PageHead>

      {!scan.data ? (
        <div className="glass centered">
          {scan.broken || scan.cancelled ? (
            <Empty icon="warning" title="Login items could not be read" text="Something blocked the check. Please try again shortly.">
              <button type="button" className="pbtn" onClick={() => void scan.rerun()}>
                Try again
              </button>
            </Empty>
          ) : (
            <>
              <Spinner />
              Looking at what starts with your Mac
            </>
          )}
        </div>
      ) : (
        <>
          <div className="speed-top stagger">
            <article className={heavyRunning ? "tile tile-speed" : "tile tile-speed is-calm"}>
              <span className="tile-glow" style={{ left: 20, top: 70, animationDuration: "6s" }} />
              <span className="tile-glow" style={{ left: 200, top: -90, animationDuration: "8s" }} />
              <div className="tile-top">
                <span className="tile-icon">
                  <Icon name="gauge" size={17} />
                </span>
                <b>Startup</b>
              </div>
              <div className="tile-number">
                <strong>{running.length}</strong>
                <em>of {all.length} on</em>
              </div>
              <div className="tile-sub">{running.length === 1 ? "1 app opens" : `${running.length} apps open`} when your computer starts</div>
              <div className="tile-foot">
                <div className="track" role="img" aria-label={`${Math.round(fraction * 100)}% of login items are on`}>
                  <span className="track-fill" style={{ width: `calc((100% - 10px) * ${Math.max(fraction, 0.04)})` }} />
                  <span className="track-knob" style={{ left: `calc((100% - 36px) * ${fraction} + 2px)` }} />
                </div>
              </div>
            </article>
            <section className="glass pad">
              <div className="section-head">
                <h2>At a glance</h2>
              </div>
              <p className="note">
                Switching an app off only keeps it from opening at login. It stays installed, and you can turn it back on whenever you like.
                {passwordGated > 0 && " Entries tagged Admin apply to every user, so macOS asks for permission first."}
              </p>
              <div className="stats">
                <div className={heavyRunning ? "stat is-hot" : "stat"}>
                  <strong>{heavyRunning}</strong>
                  <span>{"heavy apps on"}</span>
                </div>
                <div className="stat">
                  <strong>{all.length - running.length}</strong>
                  <span>turned off</span>
                </div>
                <div className="stat">
                  <strong>{managed > 0 || passwordGated === 0 ? managed : passwordGated}</strong>
                  <span>{managed > 0 || passwordGated === 0 ? "locked by the system" : "need admin rights"}</span>
                </div>
              </div>
            </section>
          </div>

          {all.length === 0 ? (
            <div className="glass">
              <Empty icon="power" title="Nothing opens at login" text="Your Mac already starts as light as it can." />
            </div>
          ) : (
            <div className="stagger group-stack">
              {TIERS.map(({ tier, heading, blurb }) => {
                const members = all.filter((i) => i.impact === tier);
                if (members.length === 0) return null;
                return (
                  <section key={tier} className="glass pad">
                    <div className="section-head">
                      <h2>{heading}</h2>
                      <span className={`status ${tier}`}>{tier.charAt(0).toUpperCase() + tier.slice(1)}</span>
                      <span className="fill" />
                      <span className="count">
                        {members.filter((m) => m.enabled).length} of {members.length} on {"·"} {blurb}
                      </span>
                    </div>
                    <div className="rows">
                      {members.map((item) => {
                        const bundle = bundleOf(item.command);
                        return <LoginRow key={item.id} item={item} pending={work.busyId === item.id} icon={bundle ? icons[bundle] : undefined} onSet={(on) => void change(item, on)} />;
                      })}
                    </div>
                  </section>
                );
              })}
            </div>
          )}
        </>
      )}
    </div>
  );
}

export default Speed;
