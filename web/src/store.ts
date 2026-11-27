// The carried states (docs/api.md "事件流"): each the whole of a small thing, by key, as the event
// stream last brought it, and what else the page keeps live in memory (the stream's own state, the
// round trip). Components read them with useCarried; nothing is a patch.
import { useRef, useSyncExternalStore } from "react";
import type { Needs, PairWindow, Context, Session, SessionState, UpdateState } from "./api/types.gen";

const states = new Map<string, unknown>();
const subs = new Map<string, Set<() => void>>();
const any = new Set<(key: string) => void>();

export function getCarried<T>(key: string): T | undefined {
  return states.get(key) as T | undefined;
}

/** Sets key's whole value; null or undefined drops it. Equal by reference is no change. */
export function setCarried(key: string, value: unknown) {
  const was = states.get(key);
  if (value === null || value === undefined) {
    if (!states.has(key)) return;
    states.delete(key);
  } else {
    if (was === value) return;
    states.set(key, value);
  }
  subs.get(key)?.forEach((f) => f());
  any.forEach((f) => f(key));
}

/** Tells f the key of each state that changes; returns what stops it. */
export function onCarried(f: (key: string) => void): () => void {
  any.add(f);
  return () => void any.delete(f);
}

const listeners = new Map<string, (f: () => void) => () => void>();
/** The subscribe of key, one function per key, so a component drawn again keeps its subscription. */
function listen(key: string) {
  let l = listeners.get(key);
  if (!l)
    listeners.set(
      key,
      (l = (f: () => void) => {
        let s = subs.get(key);
        if (!s) subs.set(key, (s = new Set()));
        s.add(f);
        return () => void s.delete(f);
      }),
    );
  return l;
}

export function useCarried<T>(key: string | null): T | undefined {
  return useSyncExternalStore(
    key ? listen(key) : noop,
    () => (key ? (states.get(key) as T | undefined) : undefined),
    () => undefined,
  );
}
const noop = () => () => {};

export const useList = () => useCarried<Session[]>("list");

/** A list as the stream brought it, each row that is as it was the object it was (by its id and
 *  its JSON): the stream gives the whole list at each change of one row, and every row drawn from
 *  it (memo, by the object) was drawn again for it. was: the rows of the list before, with their
 *  JSON; kept for the next. */
export function sharedRows<T extends { id: string }>(next: T[], was: Map<string, { raw: string; row: T }>): T[] {
  const now = new Map<string, { raw: string; row: T }>();
  const out = next.map((row) => {
    const raw = JSON.stringify(row);
    const old = was.get(row.id);
    const it = old && old.raw === raw ? old : { raw, row };
    now.set(row.id, it);
    return it.row;
  });
  was.clear();
  for (const [k, v] of now) was.set(k, v);
  return out;
}

const byId = new WeakMap<Session[], Map<string, Session>>();
const rowOf = (id: string) => {
  const all = states.get("list") as Session[] | undefined;
  if (!all) return undefined;
  let at = byId.get(all);
  if (!at) byId.set(all, (at = new Map(all.map((s) => [s.id, s]))));
  return at.get(id);
};
/** Session id's row of the list (none: not listed, or no id): what reads one row is drawn again as
 *  that row changes, not as any row of the list does (a long channel's every message read the
 *  list, and was drawn again at each change of any session). */
export function useListRow(id: string | null | undefined): Session | undefined {
  return useSyncExternalStore(
    id ? listen("list") : noop,
    () => (id ? rowOf(id) : undefined),
    () => undefined,
  );
}
/** pick of key's state (a string, a number, a boolean): drawn again as that changes, not as
 *  anything else of the state does. */
export function useCarriedPick<T, V extends string | number | boolean | undefined>(key: string | null, pick: (v: T | undefined) => V): V {
  return useSyncExternalStore(
    key ? listen(key) : noop,
    () => pick(key ? (states.get(key) as T | undefined) : undefined),
    () => pick(undefined),
  );
}
/** These sessions as the list has them (none: not listed), the same array while each is the same
 *  row: a row drawn from a few of the list's rows is drawn again as one of those changes, not as
 *  any does. No ids: undefined, and nothing listened to. */
export function useListed(ids: readonly string[] | undefined): (Session | undefined)[] | undefined {
  const last = useRef<(Session | undefined)[] | undefined>(undefined);
  const on = !!ids?.length;
  return useSyncExternalStore(
    on ? listen("list") : noop,
    () => {
      if (!on) return undefined;
      const was = last.current;
      if (was && was.length === ids!.length && ids!.every((id, i) => was[i] === rowOf(id))) return was;
      return (last.current = ids!.map(rowOf));
    },
    () => undefined,
  );
}
export const useSession = (id: string | null) => useCarried<SessionState>(id ? `session:${id}` : null);
export const useContextState = (id: string | null) => useCarried<Context>(id ? `context:${id}` : null);
/** Who is in each folder group's voice (voice/state.ts Rooms), by the group's id. */
export const useVoiceRooms = () => useCarried<Record<string, { id: number; name: string }[]>>("voice");
/** Whether a newer Iridium is out, and how installing it goes. */
export const useUpdateState = () => useCarried<UpdateState>("update");
/** What agents' ir asked a panel for (invites, tokens); none for a terminal that may not answer. */
export const useConfirms = () => useCarried<Needs>("needs")?.pending ?? NONE;
const NONE: Needs["pending"] = [];
/** Pairing: whether apps may ask to pair (on till turned off). */
export const usePairing = () => useCarried<PairWindow>("pairing");

/** The stream: null before it first opens, then whether it is open; rtt, the last round trip in ms. */
export type Link = { live: boolean | null; rtt: number | null };
export const useLink = () => useCarried<Link>("link") ?? { live: null, rtt: null };
