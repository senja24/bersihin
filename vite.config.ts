import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// The dev server injects React Refresh's preamble as an inline script and
// talks to its HMR socket, which the policy in index.html blocks. Allow both
// while serving only; builds keep the strict policy (see scripts/check-csp.ts).
const devScriptPolicy: Plugin = {
  name: "bersihin:dev-csp",
  apply: "serve",
  transformIndexHtml: (html) =>
    html
      .replace("script-src 'self';", "script-src 'self' 'unsafe-inline';")
      .replace("connect-src 'self';", "connect-src 'self' http://localhost:5173 ws://localhost:5173;"),
};

export default defineConfig({
  plugins: [react(), devScriptPolicy],
  // Scan only our page for dependencies, not the HTML under reference/.
  optimizeDeps: { entries: ["index.html"] },
  server: {
    // The app loads this address during `mygo dev` (devUrl in mygo.config.ts).
    port: 5173,
    strictPort: true,
    // The development app and the packaged builds are not the page's.
    watch: { ignored: ["**/.mygo/**", "**/build/**", "**/reference/**"] },
  },
});
