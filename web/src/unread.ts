// Unread (Rhodium spec web-maximize "未读和跑完提醒"): a session whose turn this page saw end while
// its chat was not in front of you is unread till it is: a dot on its row, a count in the title and
// the app's badge. In memory alone. The page shows no notification of its own: Web Push does, with
// the page open or not (the user, 2026-11-08).
import { useSyncExternalStore } from "react";
import type { Session } from "./api/types.gen";
import { route } from "./route";
import { lampOf } from "./ui";

export type Here = { chat: string; visible: boolean; focused: boolean };
const page = (): Here => {
  const r = route();
  return { chat: r.name === "chat" ? r.id : "", visible: document.visibilityState === "visible", focused: document.hasFocus() };
};

const unread = new Set<string>();
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

const BUSY = new Set(["running", "compacting", "starting"]);

/** Follows the list: a session going from running to idle is unread unless its chat is in front
 *  of you. here: the page now. Returns what stops it. */
export function watchUnread(here: () => Here = page) {
  const states = new Map<string, string>();
  const look = () => {
    const h = here();
    if (!h.visible || !h.chat) return;
    if (unread.delete(h.chat)) changed();
  };
  const take = (list: Session[]) => {
    const h = here();
    for (const s of list) {
      const was = states.get(s.id);
      const now = lampOf(s);
      states.set(s.id, now);
      if (was === undefined || was === now) continue;
      if (!BUSY.has(was) || (now !== "idle" && now !== "error")) continue;
      if (!(h.visible && h.chat === s.id) && !unread.has(s.id)) (unread.add(s.id), changed());
    }
  };
  for (const e of ["hashchange", "focus", "blur"]) addEventListener(e, look);
  document.addEventListener("visibilitychange", look);
  look();
  return {
    take,
    stop: () => {
      for (const e of ["hashchange", "focus", "blur"]) removeEventListener(e, look);
      document.removeEventListener("visibilitychange", look);
      unread.clear();
    },
  };
}
