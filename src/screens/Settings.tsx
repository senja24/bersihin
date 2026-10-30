import { api } from "../api";
import { BrandMark, Icon } from "../components/Icon";
import { PageHead, Segmented, Spinner, Switch } from "../components/ui";
import { messageOf } from "../lib/format";
import { usePatch } from "../lib/hooks";
import { useApp } from "../store";
import type { AiProvider, Background } from "../types";
import "./system.css";

const WALLPAPERS: { value: Background; label: string }[] = [
  { value: "mist", label: "Mist" },
  { value: "grid", label: "Grid" },
  { value: "dots", label: "Dots" },
  { value: "grain", label: "Grain" },
];
const LIMITS = ["100", "250", "500", "1000"] as const;
type Limit = (typeof LIMITS)[number];
const VENDORS: { id: AiProvider; name: string; keyPage: string }[] = [
  { id: "claude", name: "Claude", keyPage: "https://console.anthropic.com/settings/keys" },
  { id: "chatgpt", name: "ChatGPT", keyPage: "https://platform.openai.com/api-keys" },
];

function Section({ icon, title, aside, children, wide }: { icon: "layers" | "broom" | "assist"; title: string; aside?: React.ReactNode; children: React.ReactNode; wide?: boolean }) {
  return (
    <section className={wide ? "glass pad wide" : "glass pad"}>
      <div className="card-title">
        <span className="glyph">
          <Icon name={icon} />
        </span>
        <h2>{title}</h2>
        {aside && <span className="fill" />}
        {aside}
      </div>
      {children}
    </section>
  );
}

function KeyEditor({ vendor, connected }: { vendor: (typeof VENDORS)[number]; connected: boolean }) {
  const { setKey, notify } = useApp();
  const [st, set] = usePatch({ open: false, draft: "", working: false });
  const { name } = vendor;

  const commit = async (value: string | null) => {
    set({ working: true });
    try {
      await setKey(vendor.id, value);
      notify(value ? `${name} key saved` : `${name} key removed`);
      set({ open: false, draft: "" });
    } catch (e) {
      notify(messageOf(e, "Could not update the key"), "err");
    } finally {
      set({ working: false });
    }
  };

  return (
    <div className="option stacked">
      <div className="option-line">
        <span className="glyph">
          <Icon name={connected ? "lock" : "assist"} />
        </span>
        <div className="row-main">
          <b>{name}</b>
          <small>{connected ? "Key kept in your Keychain" : "No key yet"}</small>
        </div>
        <span className={connected ? "status safe" : "status"}>{connected ? "Connected" : "Not set"}</span>
        {st.working && !st.open && <Spinner />}
        {!st.working && !st.open && (
          <>
            <button type="button" className="pbtn sm" onClick={() => set({ open: true })}>
              {connected ? "Replace" : "Add key"}
            </button>
            {connected && (
              <button type="button" className="pbtn sm quiet danger" onClick={() => void commit(null)}>
                Remove
              </button>
            )}
          </>
        )}
      </div>
      {st.open && (
        <div className="key-form rise">
          <input
            className="field"
            type="password"
            autoFocus
            autoComplete="off"
            spellCheck={false}
            placeholder={`Paste your ${name} key`} aria-label={`${name} API key`}
            value={st.draft}
            onChange={(e) => set({ draft: e.target.value })}
            onKeyDown={(e) => {
              if (e.key === "Enter" && st.draft.trim()) void commit(st.draft.trim());
              if (e.key === "Escape") set({ open: false });
            }}
          />
          <button type="button" className="pbtn sm solid" disabled={st.working || !st.draft.trim()} onClick={() => void commit(st.draft.trim())}>
            {st.working ? <Spinner light size={14} /> : "Save"}
          </button>
          <button type="button" className="pbtn sm quiet" disabled={st.working} onClick={() => set({ open: false })}>
            Cancel
          </button>
          <button type="button" className="text-link" onClick={() => api.openExternal(vendor.keyPage).catch(() => notify("Could not open the browser", "err"))}>
            Get a key
            <Icon name="arrow" size={14} />
          </button>
        </div>
      )}
    </div>
  );
}

function Settings() {
  const { prefs, setPref, machine, notify } = useApp();
  const [saving, set] = usePatch<{ field: string | null }>({ field: null });

  const persist = async (field: string, change: Parameters<typeof setPref>[0]) => {
    set({ field });
    try {
      await setPref(change);
    } catch (e) {
      notify(messageOf(e, "Could not save the setting"), "err");
    } finally {
      set({ field: null });
    }
  };
  const busyMark = (field: string) => (saving.field === field ? <Spinner size={16} /> : null);

  if (!prefs) {
    return (
      <div className="screen">
        <PageHead title="Settings" />
        <div className="glass centered">
          <Spinner />
          Loading your settings
        </div>
      </div>
    );
  }

  return (
    <div className="screen">
      <PageHead title="Settings" sub="Make Bersihin your own. Every change is saved as you make it." />
      <div className="settings-grid stagger">
        <Section icon="layers" title="Appearance">
          <div className="option stacked">
            <div className="option-line">
              <div className="row-main">
                <b>Background</b>
                <small>The scenery behind each screen</small>
              </div>
              {busyMark("background")}
              <Segmented<Background> label="Background" value={prefs.background} options={WALLPAPERS} onChange={(background) => void persist("background", { background })} />
            </div>
            <div className="swatches" aria-hidden="true">
              {WALLPAPERS.map((w) => (
                <button type="button" key={w.value} tabIndex={-1} className={`swatch is-${w.value}`} aria-pressed={prefs.background === w.value} onClick={() => void persist("background", { background: w.value })}>
                  <span>{w.label}</span>
                </button>
              ))}
            </div>
          </div>
        </Section>

        <Section icon="broom" title="Cleaning">
          <div className="option">
            <div className="row-main">
              <b>Confirm before cleaning</b>
              <small>Preview what goes and wait for your approval</small>
            </div>
            {busyMark("confirm")}
            <Switch checked={prefs.confirmBeforeClean} label="Ask before cleaning" onChange={(confirmBeforeClean) => void persist("confirm", { confirmBeforeClean })} />
          </div>
          <div className="option stacked">
            <div className="option-line">
              <div className="row-main">
                <b>Large file size</b>
                <small>Anything above this appears under Large Files</small>
              </div>
              {busyMark("large")}
            </div>
            <Segmented<Limit>
              label="Large file size"
              value={String(prefs.largeFileMinMb) as Limit}
              options={LIMITS.map((l) => ({ value: l, label: l === "1000" ? "1 GB" : `${l} MB` }))}
              onChange={(l) => void persist("large", { largeFileMinMb: Number(l) })}
            />
          </div>
          <p className="note">Caches and temporary files are removed permanently. Your own files always go to the Trash, where you can restore them.</p>
        </Section>

        <Section
          wide
          icon="assist"
          title="Assistant"
          aside={
            <>
              {busyMark("provider")}
              <Segmented<AiProvider> label="Assistant provider" value={prefs.aiProvider} options={VENDORS.map((v) => ({ value: v.id, label: v.name }))} onChange={(aiProvider) => void persist("provider", { aiProvider })} />
            </>
          }
        >
          {VENDORS.map((v) => (
            <KeyEditor key={v.id} vendor={v} connected={v.id === "claude" ? prefs.hasClaudeKey : prefs.hasOpenAiKey} />
          ))}
          <p className="privacy">
            <Icon name="shield" size={16} />
            Only file names, paths and sizes leave your Mac {"—"} file contents never do.
          </p>
        </Section>

        <section className="glass pad wide">
          <div className="about">
            <BrandMark size={44} />
            <div className="row-main">
              <h3>Bersihin</h3>
              <p className="mono">{machine ? `Version ${machine.appVersion} · ${machine.osVersion} · ${machine.arch}` : "Reading version"}</p>
              <span className="made-with">
                <Icon name="heart" size={14} />
                Made with care, to keep your Mac light.
              </span>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}

export default Settings;
