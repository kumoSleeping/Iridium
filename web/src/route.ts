// Where the page is, after the address's #: "#/" nothing open, "#/s/<id>" a session's chat,
// "#/settings" the settings. One main pane either way, the chat; the settings and the terminals are
// a dialog over it (the user, 2026-11-06), the chat under it the one open before.
import { useSyncExternalStore } from "react";

export type Route = { name: "home" } | { name: "chat"; id: string } | { name: "settings" } | { name: "terminals" } | { name: "secrets" } | { name: "usage"; terminal?: string };

export function parse(hash: string): Route {
  const h = hash.replace(/^#/, "");
  const chat = /^\/s\/([^/?]+)/.exec(h);
  if (chat) return { name: "chat", id: decodeURIComponent(chat[1]!) };
  if (h.startsWith("/settings/terminals")) return { name: "terminals" };
  if (h.startsWith("/settings/secrets")) return { name: "secrets" };
  const usage = /^\/settings\/usage(?:\/([^/?]+))?/.exec(h);
  if (usage) return usage[1] ? { name: "usage", terminal: decodeURIComponent(usage[1]) } : { name: "usage" };
  if (h.startsWith("/settings")) return { name: "settings" };
  return { name: "home" };
}

export const hashOf = (r: Route) => (r.name === "chat" ? `#/s/${encodeURIComponent(r.id)}` : r.name === "home" ? "#/" : r.name === "terminals" ? "#/settings/terminals" : r.name === "secrets" ? "#/settings/secrets" : r.name === "usage" ? `#/settings/usage${r.terminal ? `/${encodeURIComponent(r.terminal)}` : ""}` : `#/${r.name}`);

/** The sessions opened lately in this tab, the latest first, for the jump box. */
export function recent(): string[] {
  try {
    return JSON.parse(sessionStorage.getItem("recent") ?? "[]");
  } catch {
    return [];
  }
}
function visited(r: Route) {
  if (r.name !== "chat") return;
  try {
    sessionStorage.setItem("recent", JSON.stringify([r.id, ...recent().filter((x) => x !== r.id)].slice(0, 20)));
  } catch {}
}

let current = parse(location.hash);
visited(current);
const subs = new Set<() => void>();

/** How the page goes from one place to the next: draw is what shows the new one (the views told,
 *  synchronously). The app sets a page turn here; by default it is drawn at once. */
export type Turn = (from: Route, to: Route, draw: () => void) => void;
let turn: Turn = (_from, _to, draw) => draw();
export const onTurn = (t: Turn) => void (turn = t);

const moved = (from: Route) => (visited(current), turn(from, current, () => subs.forEach((f) => f())));
addEventListener("hashchange", () => {
  const from = current;
  current = parse(location.hash);
  moved(from);
});

export const route = () => current;
/** The settings and the terminals: a dialog over the chat, not a page of their own. */
export const over = (r: Route) => r.name === "settings" || r.name === "terminals" || r.name === "secrets" || r.name === "usage";
export const useRoute = () =>
  useSyncExternalStore(
    (f) => (subs.add(f), () => void subs.delete(f)),
    () => current,
  );

/** Goes to r, a step of its own unless replace. The step's state marks it as the page's own, so the
 *  phone's back key knows it may go back rather than leave. */
export function go(r: Route, replace = false) {
  const hash = hashOf(r);
  if (hash === location.hash || (hash === "#/" && location.hash === "")) return;
  // depth: how many of the dialog's steps lie above the page under it, for closing it at once.
  const depth = !over(r) ? 0 : replace ? (history.state?.depth ?? 0) : over(current) ? (history.state?.depth ?? 0) + 1 : 1;
  if (replace) history.replaceState({ ir: true, depth }, "", hash);
  else history.pushState({ ir: true, depth }, "", hash);
  const from = current;
  current = parse(hash);
  moved(from);
}

/** Back to the list: the step before if this page made it, else in place. */
export function back() {
  if (history.state?.ir) history.back();
  else go({ name: "home" }, true);
}

/** The dialog closed: back past its steps to the page under it, else that page in its place. */
export function close(under: Route) {
  const d = history.state?.ir ? (history.state.depth ?? 0) : 0;
  if (d > 0) history.go(-d);
  else go(under, true);
}

export const openChat = (id: string) => go({ name: "chat", id }, route().name === "chat");
