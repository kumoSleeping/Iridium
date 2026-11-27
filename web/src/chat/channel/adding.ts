// A channel just made with no agent yet (create.tsx): the + beside its members' key opens on it
// by itself, once (top.tsx): adding its agents is what there is to do in it.
let asked: string | null = null;
const heard = new Set<() => void>();

/** The + of channel id is to open once its bar is drawn. */
export function askAdd(id: string): void {
  made.add(id);
  asked = id;
  heard.forEach((f) => f());
}

/** Whether channel id's + was asked to open: true once, then no more. */
export function takeAdd(id: string): boolean {
  if (asked !== id) return false;
  asked = null;
  return true;
}

/** Told as one is asked. Returns what stops it. */
export function onAskAdd(fn: () => void): () => void {
  heard.add(fn);
  return () => void heard.delete(fn);
}

// Made from a row of the list (a group's "+", a subfolder of it): the list stays where it is as
// the channel opens, not scrolled to its row (a subfolder's is at the group's foot: kumo, 2026-11-25).
const still = new Set<string>();
export const keepList = (id: string) => void still.add(id);
export const listKept = (id: string) => still.has(id);

// Made in this page: nothing is said in it yet, known before its log has come (the splash is
// there from its first draw, the empty pane's own going on in it: splash/).
const made = new Set<string>();
export const justMade = (id: string) => made.has(id);

// The phone's ⋯ 添加 Agent (kumo, 2026-11-26): the panel the computer's + opens, at the bar's right.
const pressed = new Set<(id: string) => void>();
/** Open channel id's 添加 Agent panel. */
export const pressAdd = (id: string) => pressed.forEach((f) => f(id));
/** Told as one is pressed. Returns what stops it. */
export function onPressAdd(fn: (id: string) => void): () => void {
  pressed.add(fn);
  return () => void pressed.delete(fn);
}
