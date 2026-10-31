import { useEffect, useRef, type KeyboardEvent } from "react";
import { api } from "../api";
import { Icon } from "../components/Icon";
import { PageHead, Segmented, Spinner } from "../components/ui";
import { PROVIDER_NAMES, isCompatible, isReady, previewKey, providerLabel, sendTarget, type CompatibleProvider } from "../lib/aiEndpoint";
import { createAiGate, type Pending } from "../lib/aiGate";
import { mergeOriginals, shortenContext } from "../lib/aiPreview";
import { formatBytes, formatDate, leafName, messageOf, tildePath } from "../lib/format";
import { usePatch } from "../lib/hooks";
import { within } from "../lib/platform";
import { newRunId } from "../lib/runId";
import { useApp } from "../store";
import type { AiAdvice, AiContext, AiProvider } from "../types";
import "./system.css";

type Turn =
  | { id: number; from: "me"; text: string }
  | { id: number; from: "ai"; advice: AiAdvice; by: string }
  | { id: number; from: "error"; text: string };
type Draft = Turn extends infer T ? (T extends { id: number } ? Omit<T, "id"> : never) : never;
type Verdict = AiAdvice["suggestions"][number]["verdict"];

const OFFICIAL = {
  claude: { keyPage: "https://console.anthropic.com/settings/keys", hint: "sk-ant-..." },
  chatgpt: { keyPage: "https://platform.openai.com/api-keys", hint: "sk-..." },
};
const SWITCH: { value: AiProvider; label: string }[] = [
  { value: "claude", label: "Claude" },
  { value: "chatgpt", label: "ChatGPT" },
  { value: "claude-compatible", label: PROVIDER_NAMES["claude-compatible"] },
  { value: "openai-compatible", label: PROVIDER_NAMES["openai-compatible"] },
];
const STARTERS = ["What can I safely delete?", "Why is my disk so full?", "Which large files haven't I used in a year?"];
const PRIVACY_NOTE = "Only names, paths and sizes are shared — file contents never are.";
const COMPAT_NOTE = "Your questions go to the address you entered, not to Anthropic or OpenAI.";
const BIG_FILE_FLOOR = 500 * 1024 * 1024;
const VERDICT_LABEL: Record<Verdict, string> = { safe: "Safe to remove", review: "Review first", keep: "Keep" };
const bare = (p: string) => p.replace(/\/+$/, "");

function SetupCard({ provider }: { provider: CompatibleProvider }) {
  const { navigate } = useApp();
  const name = PROVIDER_NAMES[provider];
  return (
    <section className="glass setup rise">
      <span className="orb orb-sm square">
        <Icon name="lock" />
      </span>
      <h3>Set up {name}</h3>
      <p>Add the address, model and API key of your service in Settings, then come back to ask your first question.</p>
      <div className="setup-foot">
        <button type="button" className="pbtn solid" onClick={() => navigate("settings")}>
          <Icon name="arrow" size={16} />
          Open Settings
        </button>
        <span className="privacy">
          <Icon name="shield" size={16} />
          {COMPAT_NOTE}
        </span>
      </div>
    </section>
  );
}

function ConnectCard({ provider }: { provider: "claude" | "chatgpt" }) {
  const { setKey, notify } = useApp();
  const [f, set] = usePatch({ draft: "", saving: false });
  const vendor = { label: PROVIDER_NAMES[provider], ...OFFICIAL[provider] };

  const store = async () => {
    const value = f.draft.trim();
    if (!value) return;
    set({ saving: true });
    try {
      await setKey(provider, value);
      set({ draft: "" });
      notify(`${vendor.label} is connected`);
    } catch (e) {
      notify(messageOf(e, "Could not save the key"), "err");
    } finally {
      set({ saving: false });
    }
  };

  return (
    <section className="glass setup rise">
      <span className="orb orb-sm square">
        <Icon name="lock" />
      </span>
      <h3>Connect {vendor.label}</h3>
      <p>
        The assistant works with your own {vendor.label} API key. It stays in your Keychain and is sent only to {vendor.label}.
      </p>
      <div className="key-form">
        <input
          className="field"
          type="password"
          autoComplete="off"
          spellCheck={false}
          placeholder={`Paste your key (${vendor.hint})`}
          aria-label={`${vendor.label} API key`}
          value={f.draft}
          onChange={(e) => set({ draft: e.target.value })}
          onKeyDown={(e) => e.key === "Enter" && void store()}
        />
        <button type="button" className="pbtn solid" disabled={f.saving || !f.draft.trim()} onClick={() => void store()}>
          {f.saving ? <Spinner light size={16} /> : <Icon name="check" size={16} />}
          Save key
        </button>
      </div>
      <div className="setup-foot">
        <button type="button" className="text-link" onClick={() => api.openExternal(vendor.keyPage).catch(() => notify("Could not open the browser", "err"))}>
          Get a key
          <Icon name="arrow" size={14} />
        </button>
        <span className="privacy">
          <Icon name="shield" size={16} />
          {PRIVACY_NOTE}
        </span>
      </div>
    </section>
  );
}

interface SuggestionProps {
  item: AiAdvice["suggestions"][number];
  full: string;
  home: string;
  isJunk: boolean;
  onReveal: () => void;
  onCleanup: () => void;
}

function Suggestion(p: SuggestionProps) {
  const { item } = p;
  return (
    <div className="suggestion">
      <span className="glyph">
        <Icon name={p.isJunk ? "folder" : "file"} />
      </span>
      <div className="suggestion-main">
        <div className="suggestion-top">
          <b>{leafName(item.path)}</b>
          <span className={`status ${item.verdict === "keep" ? "risk" : item.verdict}`}>{VERDICT_LABEL[item.verdict]}</span>
        </div>
        <div className="suggestion-path" title={item.path}>
          {tildePath(p.full, p.home)}
        </div>
        <div className="suggestion-why">{item.reason}</div>
        <div className="suggestion-actions">
          <button type="button" className="pbtn sm" onClick={p.onReveal}>
            <Icon name="reveal" size={16} />
            Reveal
          </button>
          {p.isJunk && item.verdict !== "keep" && (
            <button type="button" className="pbtn sm quiet" onClick={p.onCleanup}>
              Clean in Cleanup
              <Icon name="arrow" size={14} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

interface PreviewProps {
  vendor: string;
  compatible: boolean;
  question: string;
  context: AiContext;
  onCancel: () => void;
  onSend: () => void;
}

/** Shows exactly what the first question of a conversation will send. */
function SendPreview({ vendor, compatible, question, context, onCancel, onSend }: PreviewProps) {
  const junk = context.junk ?? [];
  const spaces = context.spaces ?? [];
  const files = context.largeFiles ?? [];
  return (
    <section className="glass send-preview rise" aria-label={`Data sent to ${vendor}`}>
      <h3>Send this to {vendor}?</h3>
      <p>{compatible ? `${COMPAT_NOTE} ` : ""}{PRIVACY_NOTE} This is everything that goes out with your question, and you are asked once per conversation.</p>
      <div className="send-preview-body">
        <b>Your question</b>
        <div className="send-preview-q">{question}</div>
        {context.disk && (
          <>
            <b>Disk</b>
            <ul>
              <li>{formatBytes(context.disk.total)} total, {formatBytes(context.disk.free)} free</li>
            </ul>
          </>
        )}
        <b>Cleanup groups ({junk.length})</b>
        <ul>
          {junk.map((j) => (
            <li key={j.name}>{j.name} · {formatBytes(j.size)} · {j.locations.join(", ")}</li>
          ))}
        </ul>
        <b>Biggest folders ({spaces.length})</b>
        <ul>
          {spaces.map((f) => (
            <li key={f.name}>{f.name} · {formatBytes(f.size)}</li>
          ))}
        </ul>
        <b>Large files ({files.length})</b>
        <ul>
          {files.map((f) => (
            <li key={f.path}>{f.path} · {formatBytes(f.size)} · {formatDate(f.modified)}</li>
          ))}
        </ul>
      </div>
      <div className="send-preview-actions">
        <button type="button" className="pbtn sm quiet" onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className="pbtn solid" onClick={onSend}>
          <Icon name="arrow" size={16} />
          Send to {vendor}
        </button>
      </div>
    </section>
  );
}

function Assistant() {
  const app = useApp();
  const { prefs, junk, maps, primary, machine, notify } = app;
  const [chat, set] = usePatch<{
    turns: Turn[]; text: string; thinking: boolean; switching: boolean; bigFiles: NonNullable<AiContext["largeFiles"]> | null;
    pending: (Pending & { before: Turn[] }) | null;
  }>({ turns: [], text: "", thinking: false, switching: false, bigFiles: null, pending: null });
  // The real path behind each shortened one the assistant saw; null when two real paths share a short form.
  const originals = useRef(new Map<string, string | null>());
  const bigKeys = useRef(new Set<string>());
  const gate = useRef(createAiGate((question: string, context: AiContext) => api.askAi(question, context))).current;
  const bottom = useRef<HTMLDivElement>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const counter = useRef(1);

  const vendorId = prefs?.aiProvider ?? "claude";
  const compatible = isCompatible(vendorId);
  const keyed = prefs ? isReady(prefs, vendorId) : false;
  const label = prefs ? providerLabel(vendorId, prefs) : PROVIDER_NAMES[vendorId];
  // What a preview approval is remembered for: the provider, and for a compatible one also its host.
  const approvalKey = prefs ? previewKey(vendorId, prefs) : vendorId;
  const home = machine?.homeDir ?? "";

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "end", behavior: "smooth" });
  }, [chat.turns, chat.thinking]);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, 160) + "px";
  }, [chat.text]);

  const pickVendor = async (next: AiProvider) => {
    if (next === vendorId) return;
    set({ switching: true });
    try {
      await app.setPref({ aiProvider: next });
    } catch (e) {
      notify(messageOf(e, "Could not switch the assistant"), "err");
    } finally {
      set({ switching: false });
    }
  };

  const loadBigFiles = async () => {
    if (chat.bigFiles) return chat.bigFiles;
    try {
      const { files: all } = await api.scanLargeFiles(newRunId(), BIG_FILE_FLOOR);
      const digest = [...all].sort((a, b) => b.size - a.size).slice(0, 60).map(({ path, size, modified }) => ({ path, size, modified }));
      set({ bigFiles: digest });
      return digest;
    } catch {
      return [];
    }
  };

  const addTurn = (d: Draft) => set({ turns: [...chat.turns, { ...d, id: counter.current++ } as Turn] });

  const fail = (turns: Turn[], e: unknown) => {
    const message = messageOf(e, "The assistant could not answer");
    set({ turns: [...turns, { id: counter.current++, from: "error", text: message }], thinking: false });
    notify(message, "err");
  };

  const ask = async (raw: string) => {
    const question = raw.trim();
    if (!question || chat.thinking || chat.pending) return;
    if (!keyed) {
      notify(compatible ? `Set up ${label} in Settings first` : `Add your ${label} key first`, "err");
      return;
    }
    if (!home) return; // paths cannot be shortened safely until the home folder is known
    const mine: Turn = { id: counter.current++, from: "me", text: question };
    set({ text: "", turns: [...chat.turns, mine], thinking: true });
    try {
      const draft: AiContext = {
        junk: (junk ?? []).filter((j) => j.size > 0).map(({ name, size, locations }) => ({ name, size, locations })),
        spaces: (maps[""]?.children ?? []).slice(0, 40).map(({ name, size }) => ({ name, size })),
        largeFiles: await loadBigFiles(),
        ...(primary ? { disk: { total: primary.total, free: primary.free } } : {}),
      };
      const { context, originals: real, files } = shortenContext(draft, home);
      mergeOriginals(originals.current, real);
      files.forEach((f) => bigKeys.current.add(f));
      const out = await gate.submit(question, context, approvalKey);
      if (out.status === "preview") {
        set({ thinking: false, pending: { ...out.pending, before: chat.turns } });
        return;
      }
      set({ turns: [...chat.turns, mine, { id: counter.current++, from: "ai", advice: out.answer, by: label }], thinking: false });
    } catch (e) {
      fail([...chat.turns, mine], e);
    }
  };

  const cancelPending = () => {
    if (!chat.pending) return;
    set({ pending: null, turns: chat.pending.before, text: chat.pending.question });
  };
  // A preview belongs to the provider and host it was made for; switching either drops it.
  useEffect(() => {
    if (chat.pending && chat.pending.vendor !== approvalKey) cancelPending();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- deliberate
  }, [approvalKey, chat.pending]);
  const sendPending = async () => {
    const { pending } = chat;
    if (!pending || pending.vendor !== approvalKey) return;
    const turns: Turn[] = [...pending.before, { id: counter.current++, from: "me", text: pending.question }];
    set({ pending: null, thinking: true, turns });
    try {
      const advice = await gate.confirm(pending);
      set({ turns: [...turns, { id: counter.current++, from: "ai", advice, by: label }], thinking: false });
    } catch (e) {
      fail(turns, e);
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void ask(chat.text);
    }
  };

  const expand = (p: string) => (/^~([\\/]|$)/.test(p) && home ? home + p.slice(1) : p);
  const junkRoots = (junk ?? []).flatMap((j) => j.locations.map((l) => bare(expand(l))));
  const insideJunk = (p: string) => junkRoots.some((r) => r && within(r, bare(p)));
  // The assistant only ever saw shortened paths; map them back to the real ones before touching the disk.
  const realPath = (p: string) => originals.current.get(p) ?? expand(p);
  const isAmbiguous = (p: string) => originals.current.get(p) === null;

  const renderTurn = (t: Turn) => {
    if (t.from === "me")
      return (
        <div key={t.id} className="msg is-user">
          <div className="bubble-text">{t.text}</div>
        </div>
      );
    if (t.from === "error")
      return (
        <div key={t.id} className="msg is-error">
          <span className="ai-icon">
            <Icon name="warning" size={18} />
          </span>
          <div className="msg-body">
            <div className="bubble-text">{t.text}</div>
          </div>
        </div>
      );
    return (
      <div key={t.id} className="msg">
        <span className="ai-icon">
          <Icon name="assist" size={18} />
        </span>
        <div className="msg-body">
          <span className="by">{t.by}</span>
          <div className="bubble-text">{t.advice.answer}</div>
          {t.advice.suggestions.length > 0 && (
            <div className="suggestions">
              {t.advice.suggestions.map((item, n) => {
                const full = realPath(item.path);
                const inBig = bigKeys.current.has(item.path);
                const ambiguous = isAmbiguous(item.path);
                return (
                  <Suggestion
                    key={`${t.id}:${n}`}
                    item={item}
                    full={full}
                    home={home}
                    isJunk={!inBig && !ambiguous && insideJunk(full)}
                    onReveal={() =>
                      ambiguous
                        ? notify("Several items share that name; open it from Large Files", "err")
                        : void api.revealPath(full).catch(() => notify("Could not show that item", "err"))
                    }
                    onCleanup={() => app.navigate("cleanup")}
                  />
                );
              })}
            </div>
          )}
        </div>
      </div>
    );
  };

  return (
    <div className="screen assistant">
      <PageHead title="Assistant" sub="Ask about your disk in everyday words. You get plain advice, and nothing is deleted unless you agree.">
        {prefs && (
          <div className="head-controls">
            {chat.switching && <Spinner />}
            <Segmented<AiProvider>
              value={vendorId} label="Assistant provider"
              onChange={(v) => void pickVendor(v)}
              options={SWITCH}
            />
          </div>
        )}
      </PageHead>

      <div className="chat">
        {!prefs && (
          <div className="centered">
            <Spinner />
            Preparing the assistant
          </div>
        )}
        {prefs && !keyed && (isCompatible(vendorId) ? <SetupCard provider={vendorId} /> : <ConnectCard provider={vendorId} />)}
        {prefs && keyed && chat.turns.length === 0 && !chat.thinking && (
          <div className="intro rise">
            <span className="orb">
              <Icon name="assist" />
            </span>
            <h2>What do you want to find out?</h2>
            <p>I can read your scan results, your biggest folders and large files, then suggest what is safe to remove and what deserves to stay.</p>
            <span className={compatible ? "privacy two-lines" : "privacy"}>
              <Icon name="shield" size={16} />
              {compatible ? (
                <span>
                  {PRIVACY_NOTE}
                  <br />
                  {COMPAT_NOTE}
                </span>
              ) : (
                PRIVACY_NOTE
              )}
            </span>
          </div>
        )}

        {chat.turns.map(renderTurn)}

        {chat.pending && (
          <SendPreview vendor={prefs ? sendTarget(vendorId, prefs) : label} compatible={compatible} question={chat.pending.question} context={chat.pending.context} onCancel={cancelPending} onSend={() => void sendPending()} />
        )}

        {chat.thinking && (
          <div className="msg">
            <span className="ai-icon">
              <Icon name="assist" size={18} />
            </span>
            <div className="bubble-text" role="status" aria-label="Thinking">
              <span className="dots">
                <i />
                <i />
                <i />
              </span>
            </div>
          </div>
        )}
        <div ref={bottom} />
      </div>

      <div className="composer">
        <div className="suggest-chips">
          {STARTERS.map((q) => (
            <button type="button" key={q} disabled={!keyed || !home || chat.thinking || !!chat.pending} onClick={() => void ask(q)}>
              <Icon name="spark" size={14} />
              {q}
            </button>
          ))}
        </div>
        <div className="compose-box">
          <textarea
            ref={box}
            rows={1}
            aria-label="Message"
            placeholder={keyed ? "Ask anything about your disk" : compatible ? `Set up ${label} in Settings to start` : `Add your ${label} key to start`}
            value={chat.text}
            disabled={!keyed}
            onChange={(e) => set({ text: e.target.value })}
            onKeyDown={onKeyDown}
          />
          <button type="button" className="send" aria-label="Send" disabled={!keyed || !home || chat.thinking || !!chat.pending || !chat.text.trim()} onClick={() => void ask(chat.text)}>
            {chat.thinking ? <Spinner light size={16} /> : <Icon name="arrow" size={18} style={{ transform: "rotate(-90deg)" }} />}
          </button>
        </div>
        <div className="compose-hint">Enter sends {"·"} Shift+Enter starts a new line</div>
      </div>
    </div>
  );
}

export default Assistant;
