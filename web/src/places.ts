// A place (a folder at this machine or at another, docs/plan.md "@"): pressed where it is named (a
// card in a chat, the chat's 在哪干活), it is shown in the list: its group opened and scrolled to, as
// a listed group holds it (sidebar.tsx); else the top "+" opens there (create.tsx). Nothing is kept.

/** machine: its ID, none for this machine; path: '/'-separated, as the page writes paths. */
export type Place = { machine?: string; path: string };

const opens = new Set<(p: Place) => void>();
const creates = new Set<(p: Place) => void>();

/** The place shown in the list, or the "+" opened there when no listed group holds it. */
export function openPlace(p: Place): void {
  if (opens.size) opens.forEach((f) => f(p));
  else createAt(p);
}

/** The top "+" opened at the place's machine, in its folder. */
export function createAt(p: Place): void {
  creates.forEach((f) => f(p));
}

/** The list's: shows a place (openPlace). Returns what stops it. */
export function onOpenPlace(fn: (p: Place) => void): () => void {
  opens.add(fn);
  return () => void opens.delete(fn);
}

/** The top "+"'s: opens at a place (createAt). Returns what stops it. */
export function onCreateAt(fn: (p: Place) => void): () => void {
  creates.add(fn);
  return () => void creates.delete(fn);
}
