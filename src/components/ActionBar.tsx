import type { ReactNode } from "react";
import type { IconName } from "../types";
import { splitBytes } from "../lib/format";
import { useConfirmGuard } from "../lib/confirmGuard";
import { Icon } from "./Icon";

/** Floating bar showing how much is picked, with the actions as children. */
export function PickedBar({ bytes, caption, children }: { bytes: number; caption: ReactNode; children: ReactNode }) {
  const parts = splitBytes(bytes);
  return (
    <div className="dock">
      <div className="dock-main">
        <div className="dock-number">
          {parts.value}
          <small>{parts.unit} selected</small>
        </div>
        <div className="dock-sub">{caption}</div>
      </div>
      {children}
    </div>
  );
}

interface AskProps {
  icon: IconName;
  warm?: boolean;
  title: string;
  note: string;
  alarming?: boolean;
  yes: string;
  danger?: boolean;
  yesDisabled?: boolean;
  onNo: () => void;
  onYes: () => void;
}

/** Floating bar that asks for a last confirmation. */
export function AskBar({ icon, warm, title, note, alarming, yes, danger, yesDisabled, onNo, onYes }: AskProps) {
  const guard = useConfirmGuard(onYes);
  return (
    <div className="dock" role="alertdialog" aria-label={title} onKeyDown={(e) => e.key === "Escape" && onNo()}>
      <span className={warm ? "glyph warm" : "glyph"}>
        <Icon name={icon} />
      </span>
      <div className="dock-main">
        <div className="dock-question">{title}</div>
        <div className={alarming ? "dock-note warn" : "dock-note"}>{note}</div>
      </div>
      <button type="button" className="pbtn quiet" onClick={onNo} autoFocus>
        Cancel
      </button>
      <button type="button" className={danger ? "pbtn solid danger" : "pbtn solid"} onClick={guard.onClick} onKeyDown={guard.onKeyDown} disabled={yesDisabled || !guard.ready}>
        {yes}
      </button>
    </div>
  );
}
