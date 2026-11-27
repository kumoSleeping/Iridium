// What the desktop app's rail shows of this Iridium (internal/desktop/rail.go; 霓虹, otae-c,
// 2026-11-26): a green dot while something runs, the number of sessions waiting on you, a short
// bar while something is unread (the title's count, unread.ts). Told to the app only, and only as
// it changes, gathered a moment (a list moving fast says it once). In a browser: nothing.
import type { Session } from "./api/types.gen";
import { ask } from "./irapp";
import { attention, onUnread } from "./unread";
import { lampOf } from "./ui";

export type RailStatus = { running: boolean; asks: number; unread: boolean };

const BUSY = new Set(["running", "compacting", "starting"]);

/** The rail's status of the sessions listed. */
export function railStatus(list: Session[], unread: number): RailStatus {
  let running = false;
  let asks = 0;
  for (const s of list) {
    if (s.archived) continue;
    const l = lampOf(s);
    if (BUSY.has(l)) running = true;
    if (l === "waiting") asks++;
  }
  return { running, asks, unread: unread > 0 };
}

let list: Session[] = [];
let said = "";
let timer: ReturnType<typeof setTimeout> | undefined;
const tell = () => {
  timer = undefined;
  const now = JSON.stringify(railStatus(list, attention()));
  if (now === said) return;
  said = now;
  ask("status", now);
};
const soon = () => void (timer ??= setTimeout(tell, 500));

/** The session list as it is now (app.tsx), for the rail. */
export function tellRail(now: Session[]) {
  list = now;
  soon();
}

onUnread(soon);
