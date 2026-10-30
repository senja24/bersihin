import type { ReactNode } from "react";
import type { IconName } from "../types";
import { Icon } from "./Icon";

export const Spinner = ({ light = false, size }: { light?: boolean; size?: number }) => (
  <span className={`spin${light ? " spin-light" : ""}`} style={size ? { width: size, height: size } : undefined} aria-hidden="true" />
);

/** The signature glass capsule used for the main action of a screen. */
export function Capsule({
  label,
  sub,
  icon = "broom",
  done = false,
  busy = false,
  disabled,
  onClick,
  onKeyDown,
}: {
  label: string;
  sub?: string;
  icon?: IconName;
  done?: boolean;
  busy?: boolean;
  disabled?: boolean;
  onClick?: (e: React.MouseEvent<HTMLButtonElement>) => void;
  onKeyDown?: (e: React.KeyboardEvent<HTMLButtonElement>) => void;
}) {
  return (
    <button type="button" className={`capsule${done ? " is-done" : ""}${busy ? " is-busy" : ""}`} onClick={onClick} onKeyDown={onKeyDown} disabled={disabled || busy}>
      <span className="capsule-orb">{busy ? <Spinner light size={18} /> : <Icon name={done ? "check" : icon} />}</span>
      <span className="capsule-text">
        <b>{label}</b>
        {sub ? <small>{sub}</small> : null}
      </span>
    </button>
  );
}

export function PageHead({ title, sub, children }: { title: string; sub?: ReactNode; children?: ReactNode }) {
  return (
    <header className="page-head rise">
      <div>
        <h1>{title}</h1>
        {sub && <p>{sub}</p>}
      </div>
      <div className="fill" />
      {children}
    </header>
  );
}

type EmptyProps = { icon: IconName; title: string; text?: ReactNode; children?: ReactNode };
export function Empty({ icon, title, text, children }: EmptyProps) {
  return (
    <div className="empty">
      <span className="orb orb-sm">
        <Icon name={icon} size={24} />
      </span>
      <h3>{title}</h3>
      {text && <p>{text}</p>}
      {children}
    </div>
  );
}

export function Meter({ value }: { value?: number | undefined }) {
  return (
    <div className={`meter${value === undefined ? " is-indeterminate" : ""}`}>
      <i style={value === undefined ? undefined : { width: `${Math.round(Math.max(0, Math.min(1, value)) * 100)}%` }} />
    </div>
  );
}

/** Centered card shown while a long scan runs. */
export function Waiting({ icon, title, text, value, current }: { icon: IconName; title: string; text: ReactNode; value?: number | undefined; current?: string | undefined }) {
  return (
    <section className="glass rise">
      <div className="waiting">
        <span className="orb">
          <Icon name={icon} />
        </span>
        <h3>{title}</h3>
        <p>{text}</p>
        <Meter value={value} />
        {current && <div className="waiting-path">{current}</div>}
      </div>
    </section>
  );
}

export function Switch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return <button type="button" role="switch" aria-checked={checked} aria-label={label} className="switch" onClick={() => onChange(!checked)} />;
}

interface SegmentedProps<T extends string> {
  value: T;
  options: { value: T; label: string }[];
  onChange: (v: T) => void;
  label: string;
}
export function Segmented<T extends string>({ value, options, onChange, label }: SegmentedProps<T>) {
  return (
    <div className="segs" role="group" aria-label={label}>
      {options.map((o) => (
        <button type="button" key={o.value} onClick={() => onChange(o.value)} aria-pressed={o.value === value}>
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Progress of a task as 0..1, or undefined when the total is not known. */
export const fraction = (p?: { done: number; total: number } | null) => (p && p.total > 0 ? p.done / p.total : undefined);

/** The big gradient number with its unit, e.g. 10.1 GB. */
export const BigNumber = ({ value, unit }: { value: string; unit: string }) => (
  <div className="big-number">
    {value}
    <small>{unit}</small>
  </div>
);
