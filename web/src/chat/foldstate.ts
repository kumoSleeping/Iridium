// Messages folded (kumo, 2026-11-21): by hand from a message's menu (折叠, 展开), kept in this
// browser per channel; and of themselves, the long ones already read before the latest rounds
// (channel/folds.ts), decided once as each is first drawn here, so nothing folds under the eyes as
// the read place moves on. A message is "m<number>", a row of an agent's own record "b<at>".
import { createContext, useRef, useSyncExternalStore } from "react";
import type { Reader } from "../api/types.gen";
import { unreadOf } from "../unread";
import { keep, kept } from "../ui";

type Way = "folded" | "open";
const KEY = (scope: string) => `folds:${scope}`;
/** How many messages a channel keeps the way of, the latest set. */
const KEEP = 400;
const ways = new Map<string, Record<string, Way>>();
const subs = new Set<() => void>();

function load(scope: string): Record<string, Way> {
  let w = ways.get(scope);
  if (w) return w;
  try {
    w = JSON.parse(kept(KEY(scope)) ?? "{}") as Record<string, Way>;
  } catch {
    w = {};
  }
  ways.set(scope, w);
  return w;
}

/** How message id of scope was last set by hand; none: as it comes of itself. */
export const foldOf = (scope: string, id: string): Way | undefined => load(scope)[id];

/** Message id of scope folded or opened by hand. */
export function setFold(scope: string, id: string, folded: boolean) {
  const w = { ...load(scope) };
  delete w[id]; // the latest set last: the oldest go first past KEEP
  w[id] = folded ? "folded" : "open";
  const ids = Object.keys(w);
  for (const k of ids.slice(0, Math.max(0, ids.length - KEEP))) delete w[k];
  ways.set(scope, w);
  keep(KEY(scope), JSON.stringify(w));
  for (const f of subs) f();
}

const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));

/** Whether message id folds of itself, as first decided here: now, the rule's answer, undefined
 *  while it cannot be told yet (the read place not known), which decides nothing. */
const once = new Map<string, boolean>();
export function foldsOnce(scope: string, id: string, now: boolean | undefined): boolean {
  const k = `${scope} ${id}`;
  const had = once.get(k);
  if (had !== undefined) return had;
  if (now === undefined) return false;
  once.set(k, now);
  return now;
}

/** Fold is a message's way: folded or not, and the way to set it by hand. */
export type Fold = { folded: boolean; set: (folded: boolean) => void };

/** Message id of scope: folded by hand, or of itself (auto) till set otherwise. */
export function useFold(scope: string, id: string, auto: boolean): Fold {
  const way = useSyncExternalStore(subscribe, () => foldOf(scope, id));
  return { folded: way ? way === "folded" : auto, set: (f) => setFold(scope, id, f) };
}

/** The fold of the message a menu (copyable.tsx) is on: its 折叠 or 展开. A menu inside it (a
 *  file's) has none. */
export const FoldMenu = createContext<Fold | null>(null);

/** How far the one looking had read channel id as its page opened: its place among the readers
 *  (me); the host's login, none of them (Iridium itself on the page), by what was unread here
 *  then, back from the log's end as it first came (end: undefined till it has). undefined till
 *  known. */
export function useReadTo(id: string, readers: readonly Reader[] | undefined, me: string | undefined, end: number | undefined): number | undefined {
  const at = useRef<{ id: string; unread: number; end?: number }>(undefined);
  if (at.current?.id !== id) at.current = { id, unread: unreadOf(id) };
  if (at.current.end === undefined && end) at.current.end = end;
  const mine = me ? readers?.find((r) => r.member === me)?.seq : undefined;
  if (mine !== undefined) return mine;
  if (!readers || at.current.end === undefined) return undefined;
  return Math.max(0, at.current.end - at.current.unread);
}
