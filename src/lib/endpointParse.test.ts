import { describe, expect, test } from "bun:test";
import { checkModel, parseEndpoint, sameHost, type ParsedEndpoint } from "./endpointParse";

const ok = (raw: string): ParsedEndpoint => {
  const r = parseEndpoint(raw);
  if (!r.ok) throw new Error(`${raw}: ${r.error}`);
  return r.value;
};
const err = (raw: string): string => {
  const r = parseEndpoint(raw);
  if (r.ok) throw new Error(`${raw} was accepted as ${r.value.base}`);
  return r.error;
};

describe("parseEndpoint normalises like Go", () => {
  test("lower-case host, no trailing slash, path kept", () => {
    expect(ok("https://OpenRouter.ai/api/v1/").base).toBe("https://openrouter.ai/api/v1");
    expect(ok("https://api.groq.com///").base).toBe("https://api.groq.com");
  });
  test("a trailing dot on the host is dropped", () => {
    expect(ok("https://example.com./v1").host).toBe("example.com");
    expect(ok("https://example.com./v1").base).toBe("https://example.com/v1");
  });
  test("explicit port is kept in the base, the effective port is by scheme", () => {
    expect(ok("https://example.com:8443/v1").base).toBe("https://example.com:8443/v1");
    expect(ok("https://example.com").port).toBe("443");
    expect(ok("http://localhost").port).toBe("80");
    expect(ok("https://example.com:443").base).toBe("https://example.com:443");
    expect(ok("https://example.com:0443").port).toBe("443");
  });
  test("IPv6 stays in brackets in the base and compresses", () => {
    expect(ok("http://[::1]:11434/v1").base).toBe("http://[::1]:11434/v1");
    expect(ok("http://[0:0:0:0:0:0:0:1]/v1").host).toBe("::1");
    expect(ok("https://[2001:DB8::1]/v1").base).toBe("https://[2001:db8::1]/v1");
  });
  test("loopback", () => {
    for (const u of ["http://localhost:1234", "http://LOCALHOST", "http://api.localhost/v1", "http://127.0.0.1", "http://127.5.5.5:80", "http://[::1]", "http://[::ffff:127.0.0.1]"])
      expect(ok(u).loopback).toBe(true);
    for (const u of ["https://openrouter.ai", "https://10.0.0.5", "https://localhost.evil.com"]) expect(ok(u).loopback).toBe(false);
  });
});

describe("parseEndpoint refuses what Go refuses", () => {
  test("empty, too long", () => {
    expect(err("")).toBe("Enter the address of the server.");
    expect(err("https://example.com/" + "a".repeat(2048))).toBe("That address is too long.");
    expect(ok("https://example.com/" + "a".repeat(2048 - 20)).host).toBe("example.com");
  });
  test("spaces, control characters, backslashes", () => {
    for (const u of ["https://exa mple.com", "https://example.com/\tv1", "https://example.com/\u0007", "https://example.com\\v1", "https://example.com/ "])
      expect(err(u)).toBe("The address may not contain spaces, control characters or backslashes.");
  });
  test("query and fragment", () => {
    expect(err("https://example.com/v1?key=1")).toBe("The address may not contain a query (?) or a fragment (#).");
    expect(err("https://example.com/v1#x")).toBe("The address may not contain a query (?) or a fragment (#).");
  });
  test("credentials", () => {
    expect(err("https://user:pw@example.com")).toContain("user name or password");
    expect(err("https://user@example.com")).toContain("user name or password");
  });
  test("scheme", () => {
    expect(err("openrouter.ai/api/v1")).toBe("The address must start with https://.");
    expect(err("ftp://example.com")).toBe("The address must start with https://.");
    expect(err("mailto:a@b.c")).toBe("That does not look like a web address.");
  });
  test("http only for this computer", () => {
    expect(err("http://example.com")).toContain("http:// is only allowed for this computer");
    expect(err("http://10.0.0.5")).toContain("http:// is only allowed for this computer");
    expect(err("http://localhost.evil.com")).toContain("http:// is only allowed for this computer");
    expect(err("http://127.0.0.1.evil.com")).toContain("http:// is only allowed for this computer");
  });
  test("ports", () => {
    for (const u of ["https://example.com:", "https://example.com:0", "https://example.com:65536", "https://example.com:abc", "https://example.com:-1"])
      expect(err(u)).toBe("The port in the address is not valid.");
    expect(ok("https://example.com:65535").port).toBe("65535");
  });
  test("dot segments in the path", () => {
    expect(err("https://example.com/v1/../x")).toBe("The address path may not contain . or .. parts.");
    expect(err("https://example.com/./v1")).toBe("The address path may not contain . or .. parts.");
    expect(ok("https://example.com/v1.0/..x").base).toBe("https://example.com/v1.0/..x");
  });
  test("unspecified, link-local and multicast addresses, also over https", () => {
    for (const u of ["https://0.0.0.0", "https://169.254.169.254/latest", "https://[::]", "https://[fe80::1]", "https://[febf::1]", "https://224.0.0.1", "https://239.1.1.1", "https://[ff02::1]", "https://[::ffff:169.254.169.254]"])
      expect(err(u)).toBe("That address is not allowed (unspecified, link-local, multicast or cloud metadata).");
    expect(ok("https://[fec0::1]").host).toBe("fec0::1");
  });
  test("shorthand IPs and bad names", () => {
    for (const u of ["https://127.1", "https://2852039166", "https://0x7f.1", "https://example.123", "https://256.1.1.1", "https://01.2.3.4"])
      expect(err(u)).toBe("Write an IP address in full (for example 127.0.0.1) or use a server name.");
    for (const u of ["https://-a.com", "https://a-.com", "https://a..com", "https://exa%6dple.com", "https://ex!ample.com", "https://" + "a".repeat(64) + ".com", "https:///v1"])
      expect(err(u)).toBe("The server name in the address is not valid.");
  });
});

describe("sameHost (host plus effective port)", () => {
  const same = (a: string, b: string) => sameHost(ok(a), ok(b));
  test("a scheme change on the same default port is another host", () => {
    expect(same("http://localhost", "https://localhost")).toBe(false);
    expect(same("http://localhost:11434", "https://localhost:11434")).toBe(true);
    expect(same("http://localhost:80", "http://localhost")).toBe(true);
  });
  test("an explicit default port equals none", () => {
    expect(same("https://example.com:443/v1", "https://example.com/v2")).toBe(true);
    expect(same("https://example.com:8443", "https://example.com")).toBe(false);
  });
  test("case, trailing dot and IPv6 spellings", () => {
    expect(same("https://EXAMPLE.com", "https://example.com.")).toBe(true);
    expect(same("http://[::1]:8080", "http://[0:0:0:0:0:0:0:1]:8080")).toBe(true);
    expect(same("http://[::1]", "http://127.0.0.1")).toBe(false);
    expect(same("https://a.example.com", "https://example.com")).toBe(false);
  });
});

describe("checkModel", () => {
  test("accepts 1 to 128 characters", () => {
    expect(checkModel("gpt-4o-mini")).toBe("");
    expect(checkModel("a".repeat(128))).toBe("");
    expect(checkModel("😀".repeat(128))).toBe("");
  });
  test("refuses empty, too long, control and invisible formatting characters", () => {
    expect(checkModel("")).toContain("1 to 128");
    expect(checkModel("a".repeat(129))).toContain("1 to 128");
    expect(checkModel("a\nb")).toContain("control or invisible");
    expect(checkModel("a​b")).toContain("control or invisible");
    expect(checkModel("a‮b")).toContain("control or invisible");
  });
});
