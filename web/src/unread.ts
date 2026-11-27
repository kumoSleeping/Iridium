// Unread (Rhodium spec web-maximize "未读和跑完提醒"; the count and 免打扰: the user, 2026-11-13):
// what was said in a session while its chat was not in front of you is counted till it is: a
// badge on its face in the list, and, for those not quiet, a count in the title and the app's
// badge. A turn this page saw end counts as one where nothing said was counted. Kept in this
// browser (the counts, and when each session last spoke as this page last saw it), so a page
// loaded again has them, and one that spoke while no page was open counts one. A session is in
// front of you too while the chat open is a channel it is in.
// No notification is shown, by the page or otherwise (Web Push is gone: kumo, 2026-11-26).
//
// Quiet (免打扰): its badge is grey and it is left out of the title's count. A session another
// app or login opened is quiet unless said otherwise, yours is not; said in its menu, kept in
// this browser.
//
// Named (docs/channels.md "有人 @ 我"): the channels where a message I have not read names me
// (the carried state "ats", kept by the daemon, the same on every device). Such a channel's
// badge is red though it is quiet, and it is in the title's count; the chat in front of me has
// read it, which the daemon is told.
import { useSyncExternalStore } from "react";
import { api } from "./api/client";
import type { ChannelAt, ChannelAts, LastSaid, Me, Session } from "./api/types.gen";
import { onMove, route } from "./route";
import { getCarried, onCarried } from "./store";
import { keep, kept, lampOf } from "./ui";

export type Here = { chat: string; visible: boolean; focused: boolean };
const page = (): Here => {
  const r = route();
  return { chat: r.name === "chat" ? r.id : "", visible: document.visibilityState === "visible", focused: document.hasFocus() };
};

const unread = new Map<string, number>();
const quiet = new Set<string>();
const told = new Set<() => void>();
/** Where I am named and have not read it, by channel; the sessions the list has. */
let named: ChannelAts = {};
const listed = new Set<string>();
/** How many the title counts: those unread and not quiet, and those that name me, quiet or not. */
export const attention = () => {
  let n = 0;
  for (const id of unread.keys()) if (!quiet.has(id) || named[id]) n++;
  for (const id of Object.keys(named)) if (!unread.has(id) && listed.has(id)) n++;
  return n;
};
const changed = () => {
  const n = attention();
  document.title = n ? `(${n}) Iridium` : "Iridium";
  (navigator as { setAppBadge?: (n: number) => Promise<void> }).setAppBadge?.(n).catch(() => {});
  told.forEach((f) => f());
};
const listen = (f: () => void) => (told.add(f), () => void told.delete(f));
/** Calls f as the counts change; returns what stops it. */
export const onUnread = listen;

/** How much of session id is unread: 0 none. */
export function useUnread(id: string): number {
  const get = () => unread.get(id) ?? 0;
  return useSyncExternalStore(listen, get, get);
}
export const unreadOf = (id: string) => unread.get(id) ?? 0;

/** The message of channel id that names me and I have not read; none: undefined. */
export function useAt(id: string): ChannelAt | undefined {
  const get = () => named[id];
  return useSyncExternalStore(listen, get, get);
}
export const atOf = (id: string) => named[id];

/** Whether session id is quiet. */
export function useQuiet(id: string): boolean {
  const get = () => quiet.has(id);
  return useSyncExternalStore(listen, get, get);
}
export const isQuiet = (id: string) => quiet.has(id);

/** A count as a badge writes it: 99+ past 99. */
export const badge = (n: number) => (n > 99 ? "99+" : String(n));

// What was said of a session's quiet by hand: {id: true | false}.
const QUIET_KEY = "ir.quiet";
let said: Record<string, boolean> | undefined;
const hand = (): Record<string, boolean> => {
  if (!said) {
    try {
      said = JSON.parse(kept(QUIET_KEY) ?? "{}") as Record<string, boolean>;
    } catch {
      said = {};
    }
  }
  return said;
};

/** Makes session id quiet or not, whatever it would be by itself. */
export function setQuiet(id: string, on: boolean) {
  said = { ...hand(), [id]: on };
  keep(QUIET_KEY, JSON.stringify(said));
  if (on === quiet.has(id)) return;
  if (on) quiet.add(id);
  else quiet.delete(id);
  changed();
}

/** Whether s is another's: an app or login that is not me opened it, or the session it was
 *  started by (its launcher, a subagent's parent) is another's. One nobody opened through Iridium
 *  is not. */
export function anothers(s: Session, me: Pick<Me, "id">, by: Map<string, Session>): boolean {
  let x = s;
  for (let i = 0; i < 16; i++) {
    const up = by.get(x.launcher || x.parent || "");
    if (!up || up === x) break;
    x = up;
  }
  return !!x.opener && x.opener !== me.id;
}

const BUSY = new Set(["running", "compacting", "starting"]);

// What is kept: {n: {id: count}, at: {id: when it last spoke}}, written a moment after a change.
const KEPT_KEY = "ir.unread";
type Kept = { n: Record<string, number>; at: Record<string, number> };
const keptNow = (): Kept => {
  try {
    const k = JSON.parse(kept(KEPT_KEY) ?? "{}") as Partial<Kept>;
    return { n: k.n ?? {}, at: k.at ?? {} };
  } catch {
    return { n: {}, at: {} };
  }
};

/** Whether what was last said is my own: typed by me, here or at another page of mine. */
const mine = (l: LastSaid, me: Pick<Me, "id">) => l.role === "user" && (!l.fromTerminal || l.fromTerminal === me.id);

/** Follows the list: what is said in a session (its last line moving on, not by me), or its turn
 *  ending with nothing counted, is unread unless its chat is in front of you. here: the page now.
 *  Returns what stops it. */
export function watchUnread(here: () => Here = page, read: (id: string, seq: number) => Promise<unknown> = (id, seq) => api.read(id, seq)) {
  const states = new Map<string, string>();
  const before = keptNow();
  const ats = new Map<string, number>(Object.entries(before.at));
  for (const [id, n] of Object.entries(before.n)) if (n > 0) unread.set(id, n);
  // The channels each session is in beside its own, as the list last said.
  let inOf = new Map<string, string[]>();
  let timer: ReturnType<typeof setTimeout> | undefined;
  const save = () => {
    timer = undefined;
    keep(KEPT_KEY, JSON.stringify({ n: Object.fromEntries(unread), at: Object.fromEntries(ats) } satisfies Kept));
  };
  const later = () => void (timer ??= setTimeout(save, 1500));
  // Another tab of this browser read a chat: read here too. Each kept its own count and wrote
  // it over the other's, so one read in a tab was unread again after the other saved.
  addEventListener("storage", (e) => {
    if (e.key !== KEPT_KEY || !e.newValue) return;
    let theirs: Kept;
    try {
      theirs = JSON.parse(e.newValue) as Kept;
    } catch {
      return;
    }
    let any = false;
    for (const id of [...unread.keys()]) if (!(theirs.n?.[id] > 0)) any = unread.delete(id) || any;
    if (any) changed();
  });
  const front = (h: Here, id: string) => h.visible && !!h.chat && (h.chat === id || !!inOf.get(id)?.includes(h.chat));
  // The chat in front of me has read what names me in it: said once for each.
  const saw = new Set<string>();
  const see = (h: Here) => {
    const a = h.visible && h.chat ? named[h.chat] : undefined;
    const key = `${h.chat}:${a?.seq}`;
    if (!a || saw.has(key)) return;
    saw.add(key);
    void read(h.chat, a.seq).catch(() => saw.delete(key));
  };
  const name = () => {
    const now = getCarried<ChannelAts>("ats") ?? {};
    if (now === named) return;
    named = now;
    changed();
    see(here());
  };
  const off = onCarried((key) => key === "ats" && name());
  name();
  const look = () => {
    const h = here();
    see(h);
    if (!h.visible || !h.chat) return;
    let any = false;
    for (const id of [...unread.keys()]) if (front(h, id)) any = unread.delete(id) || any;
    if (any) (changed(), later());
  };
  const take = (list: Session[], me: Pick<Me, "id"> = { id: "local" }) => {
    const h = here();
    const by = new Map(list.map((s) => [s.id, s]));
    const byHand = hand();
    let moved = false;
    inOf = new Map(list.flatMap((s) => (s.in?.length ? [[s.id, s.in] as [string, string[]]] : [])));
    // Those with a row: not archived, not one opened for a channel it is still in (tree.ts memberOf).
    const shown = list.filter((s) => !s.archived && !(s.spawned?.channel && s.in?.includes(s.spawned.channel))).map((s) => s.id);
    if (shown.length !== listed.size || shown.some((id) => !listed.has(id))) {
      listed.clear();
      for (const id of shown) listed.add(id);
      if (Object.keys(named).length) moved = true;
    }
    // Those the list no longer has are forgotten.
    for (const id of ats.keys()) if (!by.has(id)) (ats.delete(id), unread.delete(id) && (moved = true), later());
    for (const s of list) {
      const q = byHand[s.id] ?? anothers(s, me, by);
      if (q !== quiet.has(s.id)) {
        if (q) quiet.add(s.id);
        else quiet.delete(s.id);
        moved = true;
      }
      const was = states.get(s.id);
      const now = lampOf(s);
      states.set(s.id, now);
      const at = s.last?.at ?? 0;
      const wasAt = ats.get(s.id);
      if (wasAt !== at) (ats.set(s.id, at), later());
      // First seen by this page: what it said since a page last saw it counts one.
      const spoke = !!s.last && wasAt !== undefined && at > wasAt && !mine(s.last, me);
      const ended = was !== undefined && was !== now && BUSY.has(was) && (now === "idle" || now === "error") && !unread.has(s.id);
      if (!(spoke || ended) || front(h, s.id)) continue;
      unread.set(s.id, (unread.get(s.id) ?? 0) + 1);
      moved = true;
    }
    if (moved) (changed(), later());
  };
  for (const e of ["focus", "blur"]) addEventListener(e, look);
  document.addEventListener("visibilitychange", look);
  // A chat opened is read at once (go moves by pushState, no hashchange).
  const unmove = onMove(look);
  look();
  return {
    take,
    stop: () => {
      for (const e of ["focus", "blur"]) removeEventListener(e, look);
      unmove();
      document.removeEventListener("visibilitychange", look);
      if (timer) (clearTimeout(timer), save());
      off();
      unread.clear();
      quiet.clear();
      listed.clear();
      named = {};
    },
  };
}
