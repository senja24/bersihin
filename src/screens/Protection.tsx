import { useEffect, useId, useState, type ReactNode } from "react";
import { api } from "../api";
import { BrandGradient, Icon } from "../components/Icon";
import { Empty, PageHead, Spinner } from "../components/ui";
import { messageOf, timeAgo } from "../lib/format";
import { useCachedScan, useTween } from "../lib/hooks";
import { protectionNote } from "../lib/systemWords";
import { useApp, usePlatform } from "../store";
import type { IconName, ProtectionStatus } from "../types";
import "./system.css";

type Verdict = "good" | "warn" | "bad";
type Flag = "on" | "off" | "unknown";

const DAY_MS = 86_400_000;
const flagOf = (v: boolean | null): Flag => (v === null ? "unknown" : v ? "on" : "off");
const freshness = (when: number | null, maxDays: number, now: number): Flag => (when === null ? "unknown" : now - when < maxDays * DAY_MS ? "on" : "off");

function Ring({ score, verdict }: { score: number; verdict: Verdict }) {
  const paint = "paint" + useId().replace(/\W/g, "");
  const target = Math.min(100, Math.max(0, Math.round(score)));
  const [arc, setArc] = useState(0);
  const counted = Math.round(useTween(target, 1200));
  useEffect(() => {
    const handle = requestAnimationFrame(() => setArc(target));
    return () => cancelAnimationFrame(handle);
  }, [target]);
  return (
    <div className={`ring is-${verdict}`} role="img" aria-label={`Protection score ${target} out of 100`}>
      <span className="ring-glow" aria-hidden="true" />
      <svg viewBox="0 0 160 160" width={160} height={160} aria-hidden="true">
        <defs>
          <BrandGradient id={paint} />
        </defs>
        <circle className="ring-track" cx="80" cy="80" r="68" pathLength={100} fill="none" strokeWidth="12" />
        <circle
          className="ring-arc"
          cx="80"
          cy="80"
          r="68"
          pathLength={100}
          fill="none"
          stroke={`url(#${paint})`}
          strokeWidth="12"
          strokeLinecap="round"
          strokeDasharray={100}
          strokeDashoffset={100 - arc}
        />
      </svg>
      <div className="ring-center">
        <strong>{counted}</strong>
        <span>/ 100</span>
      </div>
    </div>
  );
}

function pill(flag: Flag, [yes, no]: [string, string] = ["On", "Off"]): ReactNode {
  if (flag === "unknown") return <span className="status">Unknown</span>;
  return flag === "on" ? <span className="status safe">{yes}</span> : <span className="status risk">{no}</span>;
}

interface Check {
  icon: IconName;
  label: string;
  detail: string;
  flag: Flag;
  tag: ReactNode;
}

function buildChecks(s: ProtectionStatus, defs: Flag, scan: Flag, now: number): Check[] {
  const notReported = "Not reported by your system";
  const av = flagOf(s.enabled);
  const rt = flagOf(s.realtime);
  const fw = flagOf(s.firewall);
  return [
    { icon: "shield", label: "Antivirus", detail: s.provider, flag: av, tag: pill(av) },
    { icon: "spark", label: "Real-time protection", detail: "Checks files the moment they are opened or downloaded", flag: rt, tag: pill(rt) },
    { icon: "globe", label: "Firewall", detail: "Blocks unwanted network connections", flag: fw, tag: pill(fw) },
    { icon: "clock", label: "Definitions updated", detail: s.definitionsUpdated ? timeAgo(s.definitionsUpdated, now) : notReported, flag: defs, tag: pill(defs, ["Up to date", "Out of date"]) },
    { icon: "scan", label: "Last scan", detail: s.lastScan ? timeAgo(s.lastScan, now) : notReported, flag: scan, tag: pill(scan, ["Recent", "A while ago"]) },
    // the platform's own further checks (SmartScreen and drive encryption on Windows), read-only like the rest
    ...(s.checks ?? []).map((c): Check => {
      const f = flagOf(c.on);
      return { icon: c.label === "SmartScreen" ? "warning" : "lock", label: c.label, detail: c.detail, flag: f, tag: pill(f) };
    }),
  ];
}

function Protection() {
  const { notify } = useApp();
  const platform = usePlatform();
  const check = useCachedScan("protection", () => api.protection(), (e) => notify(messageOf(e, "Could not check protection"), "err"));
  const s = check.data;
  const header = <PageHead title="Protection" sub="A quick look at the security your system already has built in." />;

  if (!s) {
    return (
      <div className="screen">
        {header}
        <div className="glass">
          {(check.broken || check.cancelled) && !check.busy ? (
            <Empty icon="warning" title="Protection check failed" text="Something blocked it. Please try again shortly.">
              <button type="button" className="pbtn" onClick={() => void check.rerun()}>
                Try again
              </button>
            </Empty>
          ) : (
            <div className="centered">
              <Spinner />
              Checking your protection
            </div>
          )}
        </div>
      </div>
    );
  }

  const now = Date.now();
  const defs = freshness(s.definitionsUpdated, 7, now);
  const lastScan = freshness(s.lastScan, 28, now);
  const problems = [!s.enabled, s.realtime === false, s.firewall === false, defs === "off"].filter(Boolean).length;
  const verdict: Verdict = problems === 0 && s.score >= 80 ? "good" : !s.enabled || problems > 1 ? "bad" : "warn";
  const headline = { good: "You are protected", warn: "Mostly protected", bad: "Needs attention" }[verdict];

  const chips: [string, Flag][] = [[s.enabled ? "Antivirus on" : "Antivirus off", flagOf(s.enabled)]];
  if (s.realtime !== null) chips.push([`Real-time ${s.realtime ? "on" : "off"}`, flagOf(s.realtime)]);
  if (s.firewall !== null) chips.push([`Firewall ${s.firewall ? "on" : "off"}`, flagOf(s.firewall)]);
  if (s.definitionsUpdated) chips.push([`Updated ${timeAgo(s.definitionsUpdated, now)}`, defs]);

  return (
    <div className="screen">
      {header}
      <section className="glass protect-hero rise" style={{ animationDelay: "0.06s" }}>
        <div className="protect-ring">
          <Ring score={s.score} verdict={verdict} />
        </div>
        <div className="protect-text">
          <span className={verdict === "good" ? "eyebrow" : `eyebrow is-${verdict}`}>
            <span className="dot" />
            {headline}
          </span>
          <h2>{s.provider}</h2>
          <ul className="facts" aria-label="Summary">
            {chips.map(([text, flag]) => (
              <li key={text} className={`is-${flag}`}>
                <i />
                {text}
              </li>
            ))}
          </ul>
          <p>{protectionNote(platform)}</p>
          <button type="button" className="pbtn" disabled={check.busy} onClick={() => void check.rerun()}>
            {check.busy ? <Spinner /> : <Icon name="history" size={16} />}
            {check.busy ? "Checking" : "Check again"}
          </button>
        </div>
      </section>

      <section className="glass pad rise" style={{ animationDelay: "0.12s" }}>
        <div className="section-head">
          <h2>Status</h2>
          <span className="fill" />
          <span className={problems ? "count is-warn" : "count"}>{problems === 0 ? "All good" : `${problems} to look at`}</span>
        </div>
        <div className="rows">
          {buildChecks(s, defs, lastScan, now).map((c) => (
            <div className="row" key={c.label}>
              <span className={`glyph state-${c.flag}`}>
                <Icon name={c.icon} />
              </span>
              <div className="row-main">
                <div className="row-title">{c.label}</div>
                <div className="row-sub" title={c.detail}>
                  {c.detail}
                </div>
              </div>
              {c.tag}
            </div>
          ))}
        </div>
      </section>

      {s.notes.length > 0 && (
        <section className="glass pad rise" style={{ animationDelay: "0.18s" }}>
          <div className="section-head">
            <h2>Notes</h2>
          </div>
          <ul className="notes">
            {s.notes.map((text) => (
              <li key={text}>
                <Icon name="spark" size={16} />
                <span>{text}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

export default Protection;
