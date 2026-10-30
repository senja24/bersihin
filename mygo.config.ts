import { defineConfig } from "mygo-cli";

export default defineConfig({
  name: "Bersihin",
  identifier: "dev.agus.bersihin",
  version: "0.1.2",
  updates: {
    // Public key only; the secret key stays outside the repository.
    publicKey: "Ql++n7Begzam9lN2yYo2S9jUCEB6FYLW1N+u/kFUdIA=",
    github: "senja24/bersihin",
  },
  // `mygo dev` runs devCommand and loads devUrl; `mygo build` runs
  // buildCommand and embeds frontendDist into the app.
  devUrl: "http://localhost:5173",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
});
