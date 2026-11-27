// The desktop app (app/, docs/app.md): in its window this page has the app's bridge
// (window._wails). Through it the page learns it is in the app and its state (the Iridiums in its
// list, this computer's terms for this one, the app's own), and asks things of it; what would give
// this Iridium more of the computer the app asks the person in a dialog of its own, never here.
import { useSyncExternalStore } from "react";
import { t } from "./i18n";
import { toast, toasts } from "./toast";

/** An Iridium in the app's list, with this computer as joined to it. */
export type AppServer = {
  url: string;
  host: string;
  current: boolean;
  local?: boolean; // this computer's own Iridium: never joined, its state in status
  joined: boolean;
  status: string; // the join's state, short
  shell: boolean;
  roots: string[]; // none: all
  agents: { name: string; on: boolean }[];
};
export type AppState = {
  app: "ir-app";
  version: string;
  name: string; // this computer's
  servers: AppServer[];
  update: string; // a new version downloaded, waiting for a restart; "": none
  updatePct?: number; // the app's update downloading: how far
  updateError?: string; // its download failed
  hasLocal?: boolean; // this computer's own Iridium is in the list
  server?: boolean; // this computer's own Iridium is on (ir server on|off)
  public?: boolean; // it is on the internet (公网访问)
  publicState?: {
    busy: boolean;
    note: string; // the last step, short
    link: string; // Cloudflare's login link, while waiting for it
    invite: string; // the first invite, once there
    error: string;
  };
  title?: string; // this Iridium's name in the app (its windows' titles); "": Iridium
  os?: string; // the app's system: darwin, windows, linux
  rail?: boolean; // the app's own key is its window's rail (internal/desktop/rail.go): none here
  error?: string; // what the last ask came to
  notice?: string;
  code?: string; // a browser login's code to check
};

type Bridge = { invoke: (msg: string) => void };
let state: AppState | null = null;
const told = new Set<() => void>();
const bridge = () => (window as unknown as { _wails?: Bridge })._wails;

/** Asks the app: verb, and what it is about. A function declaration, not a const: the module's
 *  own first lines ask already (tellTheme), before a const below them would be set, and that threw
 *  on the page's load, leaving it blank (kumo, 2026-11-22). */
export function ask(verb: string, arg = "") {
  return bridge()?.invoke(`ir:${verb}${arg ? ":" + arg : ""}`);
}

/** A download the app tells of (bridge.go guard): its toast, by the download's id, as it goes. The
 *  web view shows none of its own (QGI, 2026-11-23: a download went on unseen). */
type Download = {
  id: string;
  name: string;
  state: "start" | "progress" | "done" | "failed" | "gone";
  pct?: number;
};
const downloads = new Map<string, string>();
function showDownload(d: Download) {
  const was = downloads.get(d.id);
  if (d.state === "start") {
    if (was) toasts.close(was);
    downloads.set(
      d.id,
      toast(t("app.downloading"), {
        description: d.name,
        mark: "busy",
        file: true,
        timeout: 0,
      }),
    );
    return;
  }
  if (d.state === "progress") {
    // How far, in its title (下载中 37%): the app fetches it, the web view says nothing of it.
    if (was)
      toasts.update(was, { title: `${t("app.downloading")} ${d.pct ?? 0}%` });
    return;
  }
  downloads.delete(d.id);
  if (d.state === "gone") return void (was && toasts.close(was));
  const next: {
    title: string;
    description: string;
    timeout: number;
    data: { mark?: "download"; error?: boolean; file?: boolean };
  } =
    d.state === "done"
      ? {
          title: t("app.downloaded"),
          description: d.name,
          timeout: 2500,
          data: { mark: "download" as const, file: true },
        }
      : {
          title: t("app.downloadFailed"),
          description: t("app.inBrowser"),
          timeout: 8000,
          data: { error: true },
        };
  if (was) toasts.update(was, next);
  else
    toast(next.title, {
      description: next.description,
      timeout: next.timeout,
      ...next.data,
    });
}

window.addEventListener("ir-app", (e) => {
  const d = (e as CustomEvent).detail as Partial<AppState> & {
    max?: boolean;
    download?: Download;
  };
  if (d.download) return showDownload(d.download);
  // Favorites and the pages side by side: crossfav.ts's, not the app's state.
  if ("favs" in d || "favput" in d || "favdrop" in d || "assemble" in d || "sidebarW" in d) return;
  // The window maximised or not (chrome.go followMax): its key's look alone.
  if (typeof d.max === "boolean") {
    max = d.max;
    for (const f of told) f();
    return;
  }
  // A full state, or only what an ask came to.
  state = d.app
    ? (d as AppState)
    : state && { ...state, error: d.error, notice: d.notice, code: d.code };
  for (const f of told) f();
});

/** The bridge comes once the page has loaded (wails:runtime-config-ready), or is there already. */
let greeted = false;
function greet() {
  const b = bridge();
  if (greeted || !b || typeof b.invoke !== "function") return;
  greeted = true;
  b.invoke("wails:runtime:ready");
  b.invoke("ir:hello");
}
window.addEventListener("wails:runtime-config-ready", greet);
greet();

/** The day or night chosen here (me.tsx keepTheme; auto: the system's) told to the app: its own
 *  pages, and every Iridium it shows, open in it (kumo, 2026-11-22). Only a choice made is told:
 *  the page as it opens follows what the app gave it. */
export function tellTheme(pref: "auto" | "light" | "dark") {
  if (greeted) ask("theme", pref);
}

let max = false;
/** Is the window maximised? */
export const useWinMax = () =>
  useSyncExternalStore(
    (f) => (told.add(f), () => void told.delete(f)),
    () => max,
    () => false,
  );

/** The app's state; null: not in the app. */
export const useApp = () =>
  useSyncExternalStore(
    (f) => (told.add(f), () => void told.delete(f)),
    () => state,
    () => null,
  );
