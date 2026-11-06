// Where the page is, after the address's #: "#/" nothing open, "#/s/<id>" a session's chat,
// "#/settings" the settings. One sidebar either way; on a phone anything but "#/" covers it, its
// back key going back.
import { useSyncExternalStore } from "react";

export type Route = { name: "home" } | { name: "chat"; id: string } | { name: "settings" } | { name: "terminals" };

export function parse(hash: string): Route {
  const h = hash.replace(/^#/, "");
  const chat = /^\/s\/([^/?]+)/.exec(h);
  if (chat) return { name: "chat", id: decodeURIComponent(chat[1]!) };
  if (h.startsWith("/settings/terminals")) return { name: "terminals" };
  if (h.startsWith("/settings")) return { name: "settings" };
  return { name: "home" };
}

export const hashOf = (r: Route) => (r.name === "chat" ? `#/s/${encodeURIComponent(r.id)}` : r.name === "home" ? "#/" : r.name === "terminals" ? "#/settings/terminals" : `#/${r.name}`);

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
  if (replace) history.replaceState({ ir: true }, "", hash);
  else history.pushState({ ir: true }, "", hash);
  const from = current;
  current = parse(hash);
  moved(from);
}

/** Back to the list: the step before if this page made it, else in place. */
export function back() {
  if (history.state?.ir) history.back();
  else go({ name: "home" }, true);
}

export const openChat = (id: string) => go({ name: "chat", id }, route().name === "chat");
