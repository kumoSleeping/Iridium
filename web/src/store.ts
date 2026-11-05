// The carried states (docs/api.md "事件流"): each the whole of a small thing, by key, as the event
// stream last brought it, and what else the page keeps live in memory (the stream's own state, the
// round trip). Components read them with useCarried; nothing is a patch.
import { useSyncExternalStore } from "react";
import type { Confirms, Context, Quota, Session, SessionState, UpdateState } from "./api/types.gen";

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
export const useSession = (id: string | null) => useCarried<SessionState>(id ? `session:${id}` : null);
export const useContextState = (id: string | null) => useCarried<Context>(id ? `context:${id}` : null);
export const useQuota = () => useCarried<Quota>("quota");
/** Whether a newer Iridium is out, and how installing it goes. */
export const useUpdateState = () => useCarried<UpdateState>("update");
/** What agents' ir asked a panel for (invites, tokens); none for a terminal that may not answer. */
export const useConfirms = () => useCarried<Confirms>("confirms")?.pending ?? NONE;
const NONE: Confirms["pending"] = [];

/** The stream: null before it first opens, then whether it is open; rtt, the last round trip in ms. */
export type Link = { live: boolean | null; rtt: number | null };
export const useLink = () => useCarried<Link>("link") ?? { live: null, rtt: null };
