import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { BrandMark, Icon } from "../components/Icon";
import { PageHead, Segmented, Spinner, Switch } from "../components/ui";
import { PROVIDER_NAMES, dropsKey, formatMs, hostChanged, isCompatible, type CompatibleProvider } from "../lib/aiEndpoint";
import { useConfirmGuard } from "../lib/confirmGuard";
import { messageOf } from "../lib/format";
import { createStaleGuard } from "../lib/staleGuard";
import { usePatch } from "../lib/hooks";
import { useApp } from "../store";
import type { AiEndpoint, AiEndpointPatch, AiProvider, AiTestResult, Background, SettingsPatch } from "../types";
import "./system.css";

const WALLPAPERS: { value: Background; label: string }[] = [
  { value: "mist", label: "Mist" },
  { value: "grid", label: "Grid" },
  { value: "dots", label: "Dots" },
  { value: "grain", label: "Grain" },
];
const LIMITS = ["100", "250", "500", "1000"] as const;
type Limit = (typeof LIMITS)[number];
type Vendor = { id: AiProvider; name: string; keyPage?: string };
const VENDORS: (Vendor & { keyPage: string })[] = [
  { id: "claude", name: "Claude", keyPage: "https://console.anthropic.com/settings/keys" },
  { id: "chatgpt", name: "ChatGPT", keyPage: "https://platform.openai.com/api-keys" },
];
const CHOICES: AiProvider[] = ["claude", "chatgpt", "claude-compatible", "openai-compatible"];
const COMPAT: Record<CompatibleProvider, { address: string; model: string }> = {
  "claude-compatible": { address: "https://api.deepseek.com/anthropic", model: "claude-sonnet-4-5" },
  "openai-compatible": { address: "https://openrouter.ai/api/v1", model: "gpt-4o-mini" },
};
const patchOf = (kind: CompatibleProvider, p: AiEndpointPatch): SettingsPatch => (kind === "claude-compatible" ? { claudeCompat: p } : { openAiCompat: p });

type Check = { running: boolean; result: AiTestResult | null; failed: string | null };
/** One "Test connection" run. It uses the key that is saved, so it never needs the key itself. */
function useConnectionTest(provider: AiProvider) {
  const [st, set] = usePatch<Check>({ running: false, result: null, failed: null });
  const guard = useRef(createStaleGuard()).current;
  const run = async (draft: AiEndpointPatch | null) => {
    const ticket = guard.start();
    set({ running: true, result: null, failed: null });
    try {
      const result = await api.testConnection(provider, draft);
      if (guard.isCurrent(ticket)) set({ result });
    } catch (e) {
      if (guard.isCurrent(ticket)) set({ failed: messageOf(e, "The test did not finish") });
    } finally {
      if (guard.isCurrent(ticket)) set({ running: false });
    }
  };
  // A test that finishes after the settings changed describes the old settings: its answer is dropped.
  return {
    ...st,
    run,
    clear: () => {
      guard.cancel();
      set({ running: false, result: null, failed: null });
    },
  };
}

function TestPill({ t }: { t: Check }) {
  if (t.running) return <Spinner />;
  if (!t.result && !t.failed) return null;
  const ok = t.result?.ok === true;
  return <span className={ok ? "status safe" : "status risk"}>{ok && t.result ? `Connected · ${formatMs(t.result.ms)}` : "Failed"}</span>;
}

/** What went wrong (or the status and host of a good answer), under the row that ran the test. */
function TestNote({ t, pill, keyRow }: { t: Check; pill?: boolean; keyRow?: boolean }) {
  if (t.running || (!t.result && !t.failed)) return null;
  const r = t.result;
  return (
    <div className="test-out rise" role="status">
      {pill && <TestPill t={t} />}
      {r && !r.ok && !(keyRow && r.code === "no_key") && <span>{r.message}</span>}
      {t.failed && <span>{t.failed}</span>}
      {r?.detail && <span className="test-detail">{r.detail}</span>}
    </div>
  );
}

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

function KeyEditor({ vendor, connected, label, testable, notice, mismatch, onChanged, onBusy }: { vendor: Vendor; connected: boolean; label?: string; testable?: boolean; notice?: string; mismatch?: boolean; onChanged?: () => void; onBusy?: (busy: boolean) => void }) {
  const { setKey, notify } = useApp();
  const [st, set] = usePatch({ open: false, draft: "", working: false, error: "" });
  const test = useConnectionTest(vendor.id);
  const { name } = vendor;

  const commit = async (value: string | null) => {
    set({ working: true, error: "" });
    onBusy?.(true);
    try {
      await setKey(vendor.id, value);
      notify(value ? `${name} key saved` : `${name} key removed`);
      set({ open: false, draft: "" });
      test.clear();
      onChanged?.();
    } catch (e) {
      set({ error: messageOf(e, "Could not update the key") });
    } finally {
      set({ working: false });
      onBusy?.(false);
    }
  };

  return (
    <div className="option stacked">
      <div className="option-line">
        <span className="glyph">
          <Icon name={connected ? "lock" : "assist"} />
        </span>
        <div className="row-main">
          <b>{label ?? name}</b>
          <small>{connected ? "Key kept in your Keychain" : "No key yet"}</small>
        </div>
        <span className={mismatch ? "status review" : connected ? "status safe" : "status"}>{mismatch ? "Not for this address" : connected ? "Connected" : "Not set"}</span>
        {st.working && !st.open && <Spinner />}
        {!st.working && !st.open && (
          <>
            {testable && connected && (
              <button type="button" className="pbtn sm quiet" disabled={test.running} onClick={() => void test.run(null)}>
                {test.running ? <Spinner size={14} /> : null}
                Test
              </button>
            )}
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
          {vendor.keyPage && (
            <button type="button" className="text-link" onClick={() => api.openExternal(vendor.keyPage!).catch(() => notify("Could not open the browser", "err"))}>
              Get a key
              <Icon name="arrow" size={14} />
            </button>
          )}
        </div>
      )}
      {(st.error || notice) && (
        <p className="form-error" role="alert">
          {st.error || notice}
        </p>
      )}
      {testable && <TestNote t={test} pill />}
    </div>
  );
}

function DropKeyConfirm({ onCancel, onGo, busy }: { onCancel: () => void; onGo: () => void; busy: boolean }) {
  const guard = useConfirmGuard(onGo);
  const cancel = useRef<HTMLButtonElement>(null);
  // The address field that had focus is disabled while this shows, so focus moves here. Not in the same tick: the Enter that
  // opened this would still be on its way and press the button that just got focus.
  useEffect(() => {
    const timer = setTimeout(() => cancel.current?.focus(), 0);
    return () => clearTimeout(timer);
  }, []);
  return (
    <div className="inline-confirm wrap" role="alert">
      <Icon name="warning" size={16} />
      <span>Changing the address removes the saved key for this provider.</span>
      <span className="fill" />
      <button ref={cancel} type="button" className="pbtn sm quiet" onClick={onCancel} onKeyDown={guard.onKeyDown}>
        Cancel
      </button>
      <button type="button" className="pbtn sm danger" onClick={guard.onClick} onKeyDown={guard.onKeyDown} disabled={!guard.ready || busy}>
        Change address
      </button>
    </div>
  );
}

/** Address, model, key and a connection test for a Claude- or OpenAI-compatible service. */
function EndpointEditor({ kind, endpoint }: { kind: CompatibleProvider; endpoint: AiEndpoint }) {
  const { setPref } = useApp();
  const meta = COMPAT[kind];
  const [st, set] = usePatch({ url: endpoint.baseUrl, model: endpoint.model, urlRev: 0, modelRev: 0, confirm: false, saving: "", error: "", keyBusy: false });
  const test = useConnectionTest(kind);
  const address = useRef<HTMLInputElement>(null);
  const revs = useRef({ url: 0, model: 0 });
  // The saved (normalised) value replaces what was typed whenever it is saved, reverted or changed from elsewhere.
  useEffect(() => {
    set({ url: endpoint.baseUrl });
  }, [endpoint.baseUrl, st.urlRev]);
  useEffect(() => {
    set({ model: endpoint.model });
  }, [endpoint.model, st.modelRev]);
  // after the key warning closes, the keyboard goes back to the address field
  const wasConfirm = useRef(false);
  useEffect(() => {
    if (wasConfirm.current && !st.confirm) address.current?.focus();
    wasConfirm.current = st.confirm;
  }, [st.confirm]);
  const revert = (field: "url" | "model") => {
    revs.current[field]++;
    set({ urlRev: revs.current.url, modelRev: revs.current.model });
  };

  const save = async (field: "baseUrl" | "model", value: string) => {
    set({ saving: field, error: "" });
    let saved = false;
    try {
      await setPref(patchOf(kind, { [field]: value }));
      saved = true;
      test.clear();
    } catch (e) {
      // what was typed stays, with the reason, so it can be fixed
      set({ error: messageOf(e, "Could not save the setting") });
    } finally {
      set({ saving: "", confirm: false });
    }
    if (saved) revert(field === "baseUrl" ? "url" : "model");
  };
  const commitUrl = () => {
    const value = st.url.trim();
    if (st.confirm || st.saving === "baseUrl" || st.keyBusy) return;
    if (value === endpoint.baseUrl) return set({ url: value });
    if (endpoint.hasKey && dropsKey(endpoint.baseUrl, value)) return set({ confirm: true });
    void save("baseUrl", value);
  };
  const commitModel = () => {
    const value = st.model.trim();
    if (st.saving === "model") return;
    if (value === endpoint.model) return set({ model: value });
    void save("model", value);
  };
  const cancelUrl = () => {
    set({ confirm: false });
    revert("url");
  };
  const unsavedHost = hostChanged(endpoint.baseUrl, st.url);
  // The last test says the key cannot be used here: a key is stored, but not for this address (or none is stored).
  const noKey = test.result?.code === "no_key";
  const keyMismatch = noKey && endpoint.hasKey;
  const keyNotice = !noKey ? "" : endpoint.hasKey ? "This key was saved for a different address. Replace it to use it here." : "Add an API key to test the connection.";

  return (
    <>
      <div className="option stacked">
        <div className="endpoint">
          <label className="ep-field">
            <b className="ep-label">
              Address
              {st.saving === "baseUrl" && <Spinner size={14} />}
            </b>
            <input
              className="field" type="url" autoComplete="off" spellCheck={false} placeholder={meta.address}
              ref={address} value={st.url} disabled={st.confirm || st.keyBusy}
              onChange={(e) => (set({ url: e.target.value }), test.clear())}
              onBlur={commitUrl}
              onKeyDown={(e) => {
                if (e.key === "Enter") commitUrl();
                if (e.key === "Escape") cancelUrl();
              }}
            />
            <small>Include /v1 if your service uses it. http:// works only for this Mac (localhost).</small>
          </label>
          <label className="ep-field">
            <b className="ep-label">
              Model
              {st.saving === "model" && <Spinner size={14} />}
            </b>
            <input
              className="field" autoComplete="off" spellCheck={false} placeholder={meta.model}
              value={st.model}
              onChange={(e) => (set({ model: e.target.value }), test.clear())}
              onBlur={commitModel}
              onKeyDown={(e) => {
                if (e.key === "Enter") commitModel();
                if (e.key === "Escape") revert("model");
              }}
            />
            <small>The model name exactly as your service lists it.</small>
          </label>
        </div>
        {st.confirm && <DropKeyConfirm busy={st.saving !== ""} onCancel={cancelUrl} onGo={() => void save("baseUrl", st.url.trim())} />}
        {st.error && (
          <p className="form-error" role="alert">
            {st.error}
          </p>
        )}
      </div>
      <KeyEditor vendor={{ id: kind, name: PROVIDER_NAMES[kind] }} label="API key" connected={endpoint.hasKey} mismatch={keyMismatch} notice={keyNotice} onChanged={test.clear} onBusy={(keyBusy) => set({ keyBusy })} />
      <div className="option stacked">
        <div className="option-line">
          <div className="row-main">
            <b>Connection</b>
            <small>{unsavedHost ? "Save the new address to test it" : "Sends one tiny request with your saved key"}</small>
          </div>
          <TestPill t={test} />
          <button
            type="button" className="pbtn sm"
            disabled={test.running || st.confirm || st.saving !== "" || unsavedHost}
            onClick={() => void test.run({ baseUrl: st.url.trim(), model: st.model.trim() })}
          >
            Test connection
          </button>
        </div>
        {/* a missing or mismatched key is explained next to the key row */}
        <TestNote t={test} keyRow />
      </div>
    </>
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
              <Segmented<AiProvider> label="Assistant provider" value={prefs.aiProvider} options={CHOICES.map((id) => ({ value: id, label: PROVIDER_NAMES[id] }))} onChange={(aiProvider) => void persist("provider", { aiProvider })} />
            </>
          }
        >
          {isCompatible(prefs.aiProvider) ? (
            <EndpointEditor key={prefs.aiProvider} kind={prefs.aiProvider} endpoint={prefs.aiProvider === "claude-compatible" ? prefs.claudeCompat : prefs.openAiCompat} />
          ) : (
            VENDORS.map((v) => <KeyEditor key={v.id} vendor={v} testable connected={v.id === "claude" ? prefs.hasClaudeKey : prefs.hasOpenAiKey} />)
          )}
          <p className="privacy">
            <Icon name="shield" size={16} />
            Only file names, paths and sizes leave your Mac {"—"} file contents never do.
          </p>
          {isCompatible(prefs.aiProvider) && (
            <p className="privacy">
              <Icon name="shield" size={16} />
              Your questions go to the address you entered, not to Anthropic or OpenAI.
            </p>
          )}
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
