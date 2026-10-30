// Fails when the Content-Security-Policy of a built index.html allows a dev origin.
// No exemption: the built page is served from the app's own origin, so 'self' covers it.
// Usage: bun scripts/check-csp.ts [path/to/index.html]
import { readFileSync } from "node:fs";

const file = process.argv[2] ?? "dist/index.html";
const html = readFileSync(file, "utf8");
const policy = /http-equiv="Content-Security-Policy"\s+content="([^"]*)"/i.exec(html)?.[1];
if (!policy) {
  console.error(`check-csp: no Content-Security-Policy found in ${file}`);
  process.exit(1);
}
if (/localhost|127\.0\.0\.1|\[::1\]/i.test(policy)) {
  console.error(`check-csp: ${file} allows a dev origin in its CSP:\n${policy}`);
  process.exit(1);
}
console.log(`check-csp: ok (${file})`);
