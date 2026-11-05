// Unread and the done reminder (Rhodium spec web-maximize "未读和跑完提醒"): a session whose turn
// this page saw end while its chat was not in front of you is unread till it is: a dot on its row,
// a count in the title and the app's badge. With the page hidden or its window not focused, a
// system notification for a turn a message from this page began, or a session needing you, till you
// have seen it. In memory alone; Web Push (settings) is the server's and works with no page open.
import { useSyncExternalStore } from "react";
import type { Session } from "./api/types.gen";
import { route } from "./route";
import { titleOf } from "./ui";

export type Here = { chat: string; visible: boolean; focused: boolean };
const page = (): Here => {
  const r = route();
  return { chat: r.name === "chat" ? r.id : "", visible: document.visibilityState === "visible", focused: document.hasFocus() };
};

const unread = new Set<string>();
const shown = new Map<string, Notification>();
const close = (id: string) => (shown.get(id)?.close(), shown.delete(id));
const told = new Set<() => void>();
const changed = () => {
  document.title = unread.size ? `(${unread.size}) Iridium` : "Iridium";
  (navigator as { setAppBadge?: (n: number) => Promise<void> }).setAppBadge?.(unread.size).catch(() => {});
  told.forEach((f) => f());
};
const listen = (f: () => void) => (told.add(f), () => void told.delete(f));

export function useUnread(id: string): boolean {
  const get = () => unread.has(id);
  return useSyncExternalStore(listen, get, get);
}
export const isUnread = (id: string) => unread.has(id);

/** Sessions in a turn a message from this page began: their end is reminded of. */
const mine = new Set<string>();
export const sentFromHere = (id: string) => (mine.add(id), close(id));

const stored = <T>(f: () => T) => {
  try {
    return f();
  } catch {
    return undefined;
  }
};
/** Which of this browser's pages is in front, as they tell each other in storage: the others remind of nothing. */
const me = crypto.randomUUID();
const leave = () => stored(() => localStorage.getItem("front") === me && localStorage.removeItem("front"));

const BUSY = new Set(["running", "compacting", "starting"]);

/** Follows the list: a session going from running to idle is unread unless its chat is in front
 *  of you; with the page away, one of yours, or one now waiting on you, is reminded of. here: the
 *  page now; open: what a reminder's click does. Returns what stops it. */
export function watchUnread(here: () => Here = page, open: (id: string) => void = (id) => (focus(), (location.hash = `#/s/${encodeURIComponent(id)}`))) {
  const states = new Map<string, string>();
  const look = () => {
    const h = here();
    if (h.visible && h.focused) stored(() => localStorage.setItem("front", me));
    else leave();
    if (!h.visible || !h.chat) return;
    close(h.chat);
    if (unread.delete(h.chat)) changed();
  };
  const take = (list: Session[]) => {
    const h = here();
    const away = !h.visible || !h.focused;
    for (const s of list) {
      const was = states.get(s.id);
      states.set(s.id, s.status);
      if (was === undefined || was === s.status) continue;
      if (was === "waiting") close(s.id);
      if (s.status === "waiting" && away) void remind(s, open, true);
      if (!BUSY.has(was) || (s.status !== "idle" && s.status !== "error")) continue;
      const yours = mine.delete(s.id);
      if (!(h.visible && h.chat === s.id) && !unread.has(s.id)) (unread.add(s.id), changed());
      if (yours && away) void remind(s, open);
    }
  };
  for (const e of ["hashchange", "focus", "blur"]) addEventListener(e, look);
  addEventListener("pagehide", leave);
  document.addEventListener("visibilitychange", look);
  look();
  return {
    take,
    stop: () => {
      for (const e of ["hashchange", "focus", "blur"]) removeEventListener(e, look);
      removeEventListener("pagehide", leave);
      document.removeEventListener("visibilitychange", look);
      unread.clear();
    },
  };
}

/** The system notification for a session's turn: its name and the start of its last reply, or,
 *  needs, that it needs you. One per session; only with leave, and only from the page in front. */
async function remind(s: Session, open: (id: string) => void, needs = false) {
  if (typeof Notification === "undefined" || Notification.permission !== "granted" || !reminding()) return;
  const there = stored(() => localStorage.getItem("front"));
  if (there && there !== me) return;
  const reply = s.last?.role === "assistant" ? [...s.last.text] : [];
  const body = needs ? (document.documentElement.lang === "en" ? "Needs you" : "需要你") : reply.length > 80 ? `${reply.slice(0, 80).join("")}…` : reply.join("");
  try {
    const n = new Notification(titleOf(s), { tag: s.id, ...(body && { body }) });
    n.onclick = () => (open(s.id), n.close());
    shown.set(s.id, n);
  } catch {}
}

/** Whether this device reminds you; on till turned off in the settings. */
export const reminding = () => stored(() => localStorage.getItem("remind")) !== "off";
export async function remindBy(on: boolean): Promise<NotificationPermission> {
  stored(() => (on ? localStorage.removeItem("remind") : localStorage.setItem("remind", "off")));
  if (typeof Notification === "undefined") return "denied";
  if (!on || Notification.permission !== "default") return Notification.permission;
  return Notification.requestPermission();
}

/** Asks for leave to remind you once on this device, as you first send: in the gesture, the only
 *  time browsers ask in earnest. */
export function askOnce(): void {
  if (typeof Notification === "undefined" || Notification.permission !== "default" || !reminding()) return;
  try {
    if (localStorage.getItem("remindAsked")) return;
    localStorage.setItem("remindAsked", "1");
  } catch {
    return;
  }
  void Notification.requestPermission().catch(() => {});
}
