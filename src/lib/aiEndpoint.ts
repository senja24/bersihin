import { parseEndpoint, sameOrigin } from "./endpointParse";
import type { AiEndpoint, AiProvider, Settings } from "../types";

export type CompatibleProvider = "claude-compatible" | "openai-compatible";

export const PROVIDER_NAMES: Record<AiProvider, string> = {
  claude: "Claude",
  chatgpt: "ChatGPT",
  "claude-compatible": "Claude-compatible",
  "openai-compatible": "OpenAI-compatible",
};

export const isCompatible = (p: AiProvider): p is CompatibleProvider => p === "claude-compatible" || p === "openai-compatible";

export const endpointOf = (prefs: Settings, p: CompatibleProvider): AiEndpoint => (p === "claude-compatible" ? prefs.claudeCompat : prefs.openAiCompat);

const parse = (url: string) => {
  const r = parseEndpoint(url.trim());
  return r.ok ? r.value : null;
};

/** Host and port of an address ("openrouter.ai", "localhost:11434"); "" when it is empty or not a web address. */
export const hostOf = (url: string): string => parse(url)?.hostPort ?? "";

/** True for this computer: localhost, 127.x.x.x and ::1. */
export function isLoopback(url: string): boolean {
  return parse(url)?.loopback ?? false;
}

/** What the assistant is called on screen: the host of a compatible service, "Local model" for this computer. */
export function providerLabel(provider: AiProvider, prefs: Settings): string {
  if (!isCompatible(provider)) return PROVIDER_NAMES[provider];
  const { baseUrl } = endpointOf(prefs, provider);
  if (isLoopback(baseUrl)) return "Local model";
  return parse(baseUrl)?.host || PROVIDER_NAMES[provider];
}

/** Who receives the data in the preview: always names the host, with the port for this computer. */
export function sendTarget(provider: AiProvider, prefs: Settings): string {
  if (!isCompatible(provider)) return PROVIDER_NAMES[provider];
  const { baseUrl } = endpointOf(prefs, provider);
  return isLoopback(baseUrl) ? `Local model (${hostOf(baseUrl)})` : providerLabel(provider, prefs);
}

/** Official providers need a key; compatible ones need an address, a model and a key. */
export function isReady(prefs: Settings, provider: AiProvider): boolean {
  if (provider === "claude") return prefs.hasClaudeKey;
  if (provider === "chatgpt") return prefs.hasOpenAiKey;
  const e = endpointOf(prefs, provider);
  return e.baseUrl.trim() !== "" && e.model.trim() !== "" && e.hasKey;
}

const authorityOf = (url: string): string => {
  const e = parse(url);
  return e ? `${e.host}:${e.port}` : "";
};

/** What a data-preview approval is remembered for: the provider, and for a compatible one also its host. */
export const previewKey = (provider: AiProvider, prefs: Settings): string => (isCompatible(provider) ? `${provider}@${authorityOf(endpointOf(prefs, provider).baseUrl)}` : provider);

/**
 * Like Go: another origin, which is another scheme, host or effective port (http 80, https 443). An address that is not
 * valid counts as another one, since Go does not know what it points at.
 */
export const hostChanged = (saved: string, next: string): boolean => {
  const a = saved.trim();
  const b = next.trim();
  if (a === "" || b === "") return a !== b;
  const [pa, pb] = [parse(a), parse(b)];
  return !pa || !pb ? a !== b : !sameOrigin(pa, pb);
};

/**
 * Like Go's Binding compare: false when both are the same address (scheme, host, effective port and path), however
 * they are spelled. The saved key belongs to exactly one such address.
 */
export const addressChanged = (saved: string, next: string): boolean => {
  const a = saved.trim();
  const b = next.trim();
  if (a === "" || b === "") return a !== b;
  const [pa, pb] = [parse(a), parse(b)];
  return !pa || !pb ? a !== b : pa.binding !== pb.binding;
};

/** True when saving `next` over `saved` deletes the saved key: the scheme, host or port changes, or the address is cleared. A new path alone keeps it. */
export function dropsKey(saved: string, next: string): boolean {
  if (saved.trim() === "") return false;
  if (next.trim() === "") return true;
  return parse(next) !== null && hostChanged(saved, next);
}

/**
 * Why the stored key is not used for the saved address, naming the hosts ("" when the key is fine or missing).
 * Go's keyHost is the host the key was saved for.
 */
export function keyMismatchNotice(e: AiEndpoint): string {
  if (e.keyState !== "other-address") return "";
  const now = hostOf(e.baseUrl);
  const tail = " If you did not change the address, it was changed outside the app. Replace the key to use it here.";
  if (e.keyHost === "" || now === "") return `This key was not saved for the saved address.${tail}`;
  if (e.keyHost === now) return `This key was saved for a different address on ${now} (another path, or http instead of https).${tail}`;
  return `This key was saved for ${e.keyHost}, but the saved address is on ${now}.${tail}`;
}

export const formatMs = (ms: number): string => (ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(1)} s`);
