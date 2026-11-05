// The page (docs/plan.md "网页"): one sidebar and a main pane, kept live by one event stream.
import { QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { queryClient } from "./api/client";
import { App } from "./app";
import "./styles.css";
import { watchUpgrade } from "./upgrade";

watchUpgrade();

// A file dropped anywhere off the chat, which attaches it, is not opened by the browser in place of the page.
const keepFile = (e: DragEvent) => e.dataTransfer?.types.includes("Files") && e.preventDefault();
addEventListener("dragover", keepFile);
addEventListener("drop", keepFile);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
