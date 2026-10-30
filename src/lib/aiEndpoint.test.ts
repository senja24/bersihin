import { describe, expect, test } from "bun:test";
import { addressChanged, dropsKey, formatMs, hostChanged, hostOf, isLoopback, isReady, keyMismatchNotice, previewKey, providerLabel, sendTarget } from "./aiEndpoint";
import type { AiEndpoint, Settings } from "../types";

const none: AiEndpoint = { baseUrl: "", model: "", hasKey: false, keyState: "none", keyHost: "" };
const prefs = (over: Partial<Settings> = {}): Settings => ({
  aiProvider: "claude", hasClaudeKey: false, hasOpenAiKey: false, claudeCompat: none, openAiCompat: none,
  claudeModel: "", openAiModel: "", defaultClaudeModel: "claude-x", defaultOpenAiModel: "gpt-x", background: "mist", largeFileMinMb: 500, confirmBeforeClean: true, ...over,
});
const ready: AiEndpoint = { baseUrl: "https://openrouter.ai/api/v1", model: "m", hasKey: true, keyState: "ok", keyHost: "" };

describe("hostOf and isLoopback", () => {
  test("host with port, empty for junk", () => {
    expect(hostOf("https://openrouter.ai/api/v1")).toBe("openrouter.ai");
    expect(hostOf("http://localhost:11434/v1")).toBe("localhost:11434");
    expect(hostOf("")).toBe("");
    expect(hostOf("openrouter.ai")).toBe("");
    expect(hostOf("ftp://x.com")).toBe("");
  });
  test("this computer", () => {
    for (const u of ["http://localhost:1234/v1", "http://127.0.0.1:8080", "http://127.5.5.5", "http://[::1]:11434/v1", "http://api.localhost/v1"]) expect(isLoopback(u)).toBe(true);
    for (const u of ["https://openrouter.ai", "http://localhost.evil.com", "http://127.0.0.1.evil.com", "http://10.0.0.5", ""]) expect(isLoopback(u)).toBe(false);
  });
});

describe("providerLabel and sendTarget", () => {
  test("official names", () => {
    expect(providerLabel("claude", prefs())).toBe("Claude");
    expect(providerLabel("chatgpt", prefs())).toBe("ChatGPT");
  });
  test("compatible: host, Local model, or the generic name before setup", () => {
    expect(providerLabel("openai-compatible", prefs({ openAiCompat: ready }))).toBe("openrouter.ai");
    expect(providerLabel("claude-compatible", prefs({ claudeCompat: { ...ready, baseUrl: "http://localhost:11434/v1" } }))).toBe("Local model");
    expect(providerLabel("claude-compatible", prefs())).toBe("Claude-compatible");
  });
  test("the preview names the host, with the port for this computer", () => {
    expect(sendTarget("openai-compatible", prefs({ openAiCompat: ready }))).toBe("openrouter.ai");
    expect(sendTarget("openai-compatible", prefs({ openAiCompat: { ...ready, baseUrl: "http://localhost:1234/v1" } }))).toBe("Local model (localhost:1234)");
  });
});

describe("isReady", () => {
  test("official: the key", () => {
    expect(isReady(prefs({ hasClaudeKey: true }), "claude")).toBe(true);
    expect(isReady(prefs({ hasClaudeKey: true }), "chatgpt")).toBe(false);
    expect(isReady(prefs({ hasOpenAiKey: true }), "chatgpt")).toBe(true);
  });
  test("compatible: address, model and key", () => {
    expect(isReady(prefs({ claudeCompat: ready }), "claude-compatible")).toBe(true);
    expect(isReady(prefs({ claudeCompat: { ...ready, hasKey: false } }), "claude-compatible")).toBe(false);
    expect(isReady(prefs({ claudeCompat: { ...ready, model: "" } }), "claude-compatible")).toBe(false);
    expect(isReady(prefs({ claudeCompat: { ...ready, baseUrl: "" } }), "claude-compatible")).toBe(false);
    expect(isReady(prefs({ claudeCompat: ready }), "openai-compatible")).toBe(false);
  });
});

describe("previewKey", () => {
  test("changes with the host, not with the path or model", () => {
    const a = previewKey("openai-compatible", prefs({ openAiCompat: ready }));
    expect(previewKey("openai-compatible", prefs({ openAiCompat: { ...ready, baseUrl: "https://openrouter.ai/other", model: "x" } }))).toBe(a);
    expect(previewKey("openai-compatible", prefs({ openAiCompat: { ...ready, baseUrl: "https://api.groq.com/openai/v1" } }))).not.toBe(a);
    expect(previewKey("openai-compatible", prefs({ openAiCompat: { ...ready, baseUrl: "http://localhost:1234" } }))).not.toBe(previewKey("openai-compatible", prefs({ openAiCompat: { ...ready, baseUrl: "http://localhost:5678" } })));
  });
  test("differs per provider and is plain for official ones", () => {
    expect(previewKey("claude", prefs())).toBe("claude");
    expect(previewKey("claude-compatible", prefs({ claudeCompat: ready }))).not.toBe(previewKey("openai-compatible", prefs({ openAiCompat: ready })));
  });
});

describe("hostChanged and dropsKey", () => {
  const saved = "https://openrouter.ai/api/v1";
  test("same host, other scheme: default ports differ, so the key goes", () => {
    expect(hostChanged("http://localhost", "https://localhost")).toBe(true);
    expect(dropsKey("http://localhost", "https://localhost")).toBe(true);
    expect(dropsKey("http://localhost:11434/v1", "https://localhost:11434/v1")).toBe(true);
    expect(dropsKey("http://localhost:11434/v1", "http://localhost:11434/v2")).toBe(false);
    expect(dropsKey("https://example.com", "https://example.com:443/v1")).toBe(false);
    expect(dropsKey("https://example.com", "https://example.com./v1")).toBe(false);
    expect(dropsKey("http://[::1]:8080", "http://[0:0:0:0:0:0:0:1]:8080/v2")).toBe(false);
  });
  test("a path-only change keeps the key", () => {
    expect(hostChanged(saved, "https://openrouter.ai/v2")).toBe(false);
    expect(hostChanged(saved, "https://OPENROUTER.ai:443/v2")).toBe(false);
    expect(dropsKey(saved, "https://openrouter.ai/v2")).toBe(false);
  });
  test("another host or port drops it", () => {
    expect(dropsKey(saved, "https://api.groq.com/openai/v1")).toBe(true);
    expect(dropsKey(saved, "https://openrouter.ai:8443/api/v1")).toBe(true);
  });
  test("clearing the address drops it, going from nothing does not", () => {
    expect(dropsKey(saved, "")).toBe(true);
    expect(dropsKey("", saved)).toBe(false);
    expect(dropsKey("", "")).toBe(false);
  });
  test("a typed host that is not an address cannot be saved, so it drops nothing, but counts as unsaved", () => {
    expect(dropsKey(saved, "groq.com")).toBe(false);
    expect(hostChanged(saved, "groq.com")).toBe(true);
    expect(hostChanged("", saved)).toBe(true);
  });
});

describe("formatMs", () => {
  test("ms under a second, seconds above", () => {
    expect(formatMs(420)).toBe("420 ms");
    expect(formatMs(1540)).toBe("1.5 s");
  });
});

describe("addressChanged (the full address a key is bound to)", () => {
  test("a new path, scheme or port is another address", () => {
    expect(addressChanged("https://gw.example/v1/a/one", "https://gw.example/v1/a/two")).toBe(true);
    expect(addressChanged("http://localhost:443", "https://localhost")).toBe(true);
    expect(addressChanged("https://x.com", "https://x.com:8443")).toBe(true);
  });
  test("other spellings of one address are the same", () => {
    expect(addressChanged("https://X.com:443/v1/", "https://x.com/v1")).toBe(false);
    expect(addressChanged("", "")).toBe(false);
    expect(addressChanged("", "https://x.com")).toBe(true);
    expect(addressChanged("https://x.com", "")).toBe(true);
  });
  test("a path change alone keeps the key (Go moves its binding)", () => {
    expect(dropsKey("https://gw.example/v1/a/one", "https://gw.example/v1/a/two")).toBe(false);
  });
});

describe("keyMismatchNotice", () => {
  const other = (over: Partial<AiEndpoint>): AiEndpoint => ({ ...ready, hasKey: false, keyState: "other-address", ...over });
  test("empty unless the key belongs to another address", () => {
    expect(keyMismatchNotice(ready)).toBe("");
    expect(keyMismatchNotice(none)).toBe("");
  });
  test("names both hosts", () => {
    const n = keyMismatchNotice(other({ keyHost: "api.example.com" }));
    expect(n).toContain("api.example.com");
    expect(n).toContain("openrouter.ai");
    expect(n).toContain("outside the app");
  });
  test("same host: another path or scheme", () => {
    expect(keyMismatchNotice(other({ keyHost: "openrouter.ai" }))).toContain("different address on openrouter.ai");
  });
  test("host unknown", () => {
    expect(keyMismatchNotice(other({ keyHost: "" }))).toContain("not saved for the saved address");
  });
});
