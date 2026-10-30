// Mirrors internal/endpoint/endpoint.go (Parse, CheckModel, SameOrigin, Binding) closely enough for demo mode and for the page to
// know, before saving, what Go will do: which addresses it refuses, and whether a change of address is another origin.
// Go is the authority; if the two ever differ, Go wins and its message is shown.

export const MAX_URL = 2048;
export const MAX_MODEL = 128;

export type ParsedEndpoint = {
  /** scheme, host, explicit port and path, no trailing slash: what Go stores */
  base: string;
  scheme: "http" | "https";
  /** lower case, no brackets, no trailing dot; IPv6 in compressed form */
  host: string;
  /** the explicit port, or 443 (https) / 80 (http) */
  port: string;
  /** host plus the explicit port only, the way people write it ("openrouter.ai", "localhost:11434") */
  hostPort: string;
  loopback: boolean;
  /** Go's Binding: scheme, host, effective port and path. A stored key belongs to exactly this. */
  binding: string;
};
export type ParseResult = { ok: true; value: ParsedEndpoint } | { ok: false; error: string };

const fail = (error: string): ParseResult => ({ ok: false, error });
const BAD_HOST = "The server name in the address is not valid.";

type Checked = { host: string; loopback: boolean } | { error: string };

function ipv4(h: string): number[] | null {
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h);
  if (!m) return null;
  const parts = m.slice(1).map((p) => ({ text: p, n: Number(p) }));
  // netip refuses leading zeros and octets over 255
  return parts.every((p) => p.n <= 255 && (p.text === "0" || !p.text.startsWith("0"))) ? parts.map((p) => p.n) : null;
}

/** Eight 16-bit groups of an IPv6 address, or null when it is not one. */
function ipv6(h: string): number[] | null {
  let host: string;
  try {
    host = new URL(`http://[${h}]/`).hostname.slice(1, -1);
  } catch {
    return null;
  }
  const [head = "", tail] = host.split("::");
  const groups = (s: string) => (s === "" ? [] : s.split(":").map((g) => parseInt(g, 16)));
  const a = groups(head);
  const b = tail === undefined ? [] : groups(tail);
  const out = tail === undefined ? a : [...a, ...Array<number>(8 - a.length - b.length).fill(0), ...b];
  return out.length === 8 && out.every((g) => Number.isInteger(g) && g >= 0 && g <= 0xffff) ? out : null;
}

function showIPv6(g: number[]): string {
  // compress the longest run of zero groups, like Go's netip
  let best = { at: -1, len: 0 };
  for (let i = 0; i < 8; ) {
    if (g[i] !== 0) {
      i++;
      continue;
    }
    let j = i;
    while (j < 8 && g[j] === 0) j++;
    if (j - i > best.len) best = { at: i, len: j - i };
    i = j;
  }
  const hex = (xs: number[]) => xs.map((x) => x.toString(16)).join(":");
  if (best.len < 2) return hex(g);
  return `${hex(g.slice(0, best.at))}::${hex(g.slice(best.at + best.len))}`;
}

function checkHost(raw: string): Checked {
  const h = (raw.endsWith(".") ? raw.slice(0, -1) : raw).toLowerCase();
  if (h === "" || h.includes("%")) return { error: BAD_HOST };
  const blocked = { error: "That address is not allowed (unspecified, link-local, multicast or cloud metadata)." };
  if (h.includes(":")) {
    const g = ipv6(h);
    if (!g) return { error: BAD_HOST };
    // an IPv4 address written in IPv6 form is judged as the IPv4 address
    const mapped = g.slice(0, 5).every((x) => x === 0) && g[5] === 0xffff;
    if (mapped) {
      const v4 = [g[6]! >> 8, g[6]! & 255, g[7]! >> 8, g[7]! & 255];
      return checkV4(v4, blocked, `${v4.join(".")}`);
    }
    if (g.every((x) => x === 0)) return blocked;
    if ((g[0]! & 0xffc0) === 0xfe80 || (g[0]! & 0xff00) === 0xff00) return blocked;
    return { host: showIPv6(g), loopback: g.slice(0, 7).every((x) => x === 0) && g[7] === 1 };
  }
  const v4 = ipv4(h);
  if (v4) return checkV4(v4, blocked, h);
  let name = h;
  if (/[^\x00-\x7f]/.test(name)) {
    try {
      name = new URL(`http://${name}/`).hostname;
    } catch {
      return { error: BAD_HOST };
    }
  }
  if (name.length > 253) return { error: BAD_HOST };
  const labels = name.split(".");
  for (const l of labels) {
    if (l === "" || l.length > 63 || l.startsWith("-") || l.endsWith("-") || !/^[a-z0-9_-]+$/.test(l)) return { error: BAD_HOST };
  }
  // a name that ends in a number is an IP in a shorthand the system resolver may still accept (127.1, 0x7f.1)
  if (/^\d/.test(labels[labels.length - 1]!)) return { error: "Write an IP address in full (for example 127.0.0.1) or use a server name." };
  return { host: name, loopback: name === "localhost" || name.endsWith(".localhost") };
}

function checkV4(v4: number[], blocked: { error: string }, text: string): Checked {
  const [a, b] = [v4[0]!, v4[1]!];
  if ((a === 0 && v4.every((x) => x === 0)) || (a === 169 && b === 254) || (a >= 224 && a <= 239)) return blocked;
  return { host: text, loopback: a === 127 };
}

/** Go's endpoint.Parse: the address as the user typed it (already trimmed), checked and normalised. */
export function parseEndpoint(raw: string): ParseResult {
  if (raw === "") return fail("Enter the address of the server.");
  if (raw.length > MAX_URL) return fail("That address is too long.");
  if (/[\s\p{Cc}\\]/u.test(raw)) return fail("The address may not contain spaces, control characters or backslashes.");
  if (/[?#]/.test(raw)) return fail("The address may not contain a query (?) or a fragment (#).");
  const m = /^([A-Za-z][A-Za-z0-9+.-]*):\/\/([^/]*)(\/.*)?$/.exec(raw);
  if (!m) return fail(/^[A-Za-z][A-Za-z0-9+.-]*:/.test(raw) ? "That does not look like a web address." : "The address must start with https://.");
  const scheme = m[1]!.toLowerCase();
  const authority = m[2]!;
  const path = m[3] ?? "";
  if (authority.includes("@")) return fail("The address may not contain a user name or password. Put the key in the API key field.");
  if (scheme !== "https" && scheme !== "http") return fail("The address must start with https://.");
  const hp = /^(\[[^\]]*\]|[^:[\]]*)(?::(.*))?$/.exec(authority);
  if (!hp) return fail("That does not look like a web address.");
  if (authority.endsWith(":")) return fail("The port in the address is not valid.");
  let explicit = "";
  if (hp[2] !== undefined) {
    const n = /^\d+$/.test(hp[2]) ? Number(hp[2]) : 0;
    if (n < 1 || n > 65535) return fail("The port in the address is not valid.");
    explicit = String(n);
  }
  const host = checkHost(hp[1]!.replace(/^\[|\]$/g, ""));
  if ("error" in host) return fail(host.error);
  if (scheme === "http" && !host.loopback) return fail("http:// is only allowed for this computer (localhost). Use https:// for any other server.");
  if (path.split("/").some((seg) => seg === "." || seg === "..")) return fail("The address path may not contain . or .. parts.");
  const shown = host.host.includes(":") ? `[${host.host}]` : host.host;
  const hostPort = explicit ? `${shown}:${explicit}` : shown;
  return {
    ok: true,
    value: {
      base: `${scheme}://${hostPort}${path.replace(/\/+$/, "")}`,
      scheme,
      host: host.host,
      port: explicit || (scheme === "http" ? "80" : "443"),
      hostPort,
      loopback: host.loopback,
      binding: `${scheme}://${shown}:${explicit || (scheme === "http" ? "80" : "443")}${path.replace(/\/+$/, "")}`,
    },
  };
}

/** Go's SameOrigin: the same scheme, host and effective port (http 80, https 443). */
export const sameOrigin = (a: ParsedEndpoint, b: ParsedEndpoint): boolean => a.scheme === b.scheme && a.host === b.host && a.port === b.port;

/**
 * Go's CheckModel: 1 to 128 characters, no control or invisible formatting characters, no line or paragraph separators,
 * no space other than U+0020 and no Hangul filler. Returns the message, or "".
 */
export function checkModel(name: string): string {
  if (name === "" || [...name].length > MAX_MODEL) return "The model name must be 1 to 128 characters long.";
  if (/[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\u115F\u1160\u3164\uFFA0]|(?! )\p{Zs}/u.test(name)) return "The model name may not contain control or invisible characters, line separators or special spaces.";
  return "";
}
