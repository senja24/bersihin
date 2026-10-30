import "@fontsource-variable/onest";
import "@fontsource-variable/unbounded";
import "@fontsource-variable/jetbrains-mono";
import "./styles/tokens.css";
import "./styles/base.css";
import "./styles/shell.css";
import "./styles/ui.css";
import "./styles/bubbles.css";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./lib/activity";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
