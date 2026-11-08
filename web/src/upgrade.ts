// A page left open over an upgrade (plan "弱网", Rhodium architecture §2): it loads itself again for
// the new build only as it comes back in front, with nothing you would lose, once per build and
// never twice within 10 s. The build is the HTML's first module script, whose name has its hash;
// the served one is read from the HTML each time the stream opens.
import { useSyncExternalStore } from "react";
import { anyUnsent } from "./chats";
import { posting, request } from "./api/client";
import { opened } from "./stream";

/** When the page last loaded itself again, and for which build, as this tab keeps it; null for
 *  storage that cannot be used. */
export function lastReload(): { at: number; build: string | null } | null {
  try {
    return { at: Number(sessionStorage.getItem("reloaded")), build: sessionStorage.getItem("reloadedFor") };
  } catch {
    return null;
  }
}
/** Stamps first, so storage that throws means no reload, not a loop. */
export function reload(build: string | null) {
  try {
    sessionStorage.setItem("reloaded", String(Date.now()));
    if (build) sessionStorage.setItem("reloadedFor", build);
  } catch {
    return;
  }
  location.reload();
}
/** Loads the page again unless it did within 10 s, for whatever reason. */
export function reloadAgain() {
  const last = lastReload();
  if (last && Date.now() - last.at >= 10_000) reload(null);
}

/** Whether to load the page again now: mine and served, the entry scripts of this page's build and
 *  of the daemon's; you just came back and are busy with nothing; not within 10 s of the last time,
 *  nor twice for one build. */
export function reloadNow(mine: string | null, served: string | null, you: { back: boolean; busy: boolean }, last: { at: number; build: string | null }, now: number): boolean {
  return mine !== null && served !== null && served !== mine && served !== last.build && now - last.at >= 10_000 && you.back && !you.busy;
}

/** The first module script an HTML loads. */
export const entryOf = (html: string) => /<script[^>]*type="module"[^>]*src="([^"]+)"/.exec(html)?.[1] ?? null;

const MINE = document.querySelector('script[type="module"][src]')?.getAttribute("src") ?? null;
let served: string | null = null;
let back = !document.hidden;
/** An update pressed on this page: once the new build is served, load it without waiting for you
 *  to come back. */
let asked = false;
const seen = new Set<() => void>();
const tell = () => seen.forEach((f) => f());

/** An update pressed here (settings, the update key): the new build is loaded as soon as it is
 *  served, unless that would lose something. */
export function expectUpgrade(on = true) {
  asked = on;
}

/** Whether the daemon serves another build than this page's, as last read (kept while the stream
 *  reopens, so the link dot does not flicker): the link dot offers 刷新. */
let stale = false;
export const useStale = () =>
  useSyncExternalStore(
    (f) => (seen.add(f), () => void seen.delete(f)),
    () => stale,
  );
/** 刷新 pressed: load the served build now. */
export const reloadServed = () => reload(served);

/** What loading again would lose: a write going, a message that failed, words in a field (the
 *  composer's only while you type in it: its draft is kept), files attached, a popup open. */
function busy() {
  const e = document.activeElement;
  const typed = [...document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea")].some((f) => f.value !== "" && f.type !== "file" && (f === e || !f.dataset.draft));
  // The settings' dialog, where 更新 was pressed, holds nothing to lose (words typed in it are
  // counted above): kept open over the restart it held the old build there, drawn against the new
  // daemon, till it was put away and the page loaded again under it.
  const popups = '[data-attachments], [role="dialog"]:not([data-settings-dialog]), [role="alertdialog"], [role="menu"]';
  return posting.count > 0 || anyUnsent() || typed || !!document.querySelector(asked ? popups : '[data-attachments], [role="dialog"], [role="alertdialog"], [role="menu"]');
}
function mayReload() {
  const last = lastReload();
  if (last && reloadNow(MINE, served, { back: back || asked, busy: busy() }, last, Date.now())) reload(served);
}

export function watchUpgrade() {
  addEventListener("vite:preloadError", reloadAgain);
  for (const t of ["pointerdown", "keydown", "wheel", "input"]) addEventListener(t, () => (back = false), { capture: true, passive: true });
  document.addEventListener("visibilitychange", () => ((back = !document.hidden), mayReload()));
  opened.add(() => {
    served = null;
    void request("/", { cache: "no-cache" })
      .then((r) => (r.ok ? r.text() : ""))
      .then((html) => ((served = entryOf(html)), (stale = MINE !== null && served !== null && served !== MINE), tell(), mayReload()))
      .catch(() => {});
  });
  setInterval(mayReload, 1_000);
}
