// The work of motion.ts useMoves and usePaced (the rules are said there). Its own file: brought
// after the first screen.
import { createElement, useEffect, useRef, useState, type ReactNode } from "react";
import { motion, reduced } from "./motion";

/** A list's rows as useMoves follows them: the order last drawn and the one being drawn; where
 *  the rows were, read before a change of order; which list it is. */
export type Moves = { drawn?: string; next?: string; before?: Spots | null; scope?: string };

/** As the list is drawn. */
export function draws(s: Moves, root: HTMLElement | null, order: string, scope = "") {
  // Another list in the same place (the next chat): nothing of it was anywhere before.
  if (scope !== (s.scope ?? "")) ((s.scope = scope), (s.drawn = undefined), (s.before = null));
  s.next = order;
  if (order !== s.drawn && s.drawn !== undefined && !s.before && root) s.before = spotsOf(root);
}

/** Once it is drawn. */
export function drawn(s: Moves, root: HTMLElement | null, enter: boolean) {
  const before = s.before;
  const moved = s.drawn !== undefined && s.next !== s.drawn;
  s.drawn = s.next;
  s.before = null;
  if (moved && before && root) play(root, before, enter);
}

/** When the user last did something (a press, a key, a drop): what changes right after is theirs,
 *  and is not made to wait. */
let actedAt = -1e9;
if (typeof window !== "undefined") for (const k of ["pointerup", "keydown", "drop"]) addEventListener(k, () => (actedAt = performance.now()), true);
const ACTED = 1500;

/** The beat: at most one change of order a beat in a list that reorders of itself. */
export const BEAT = 700;
/** How long a change of order waits (ms; 0: it is drawn now), the last drawn at last: the first
 *  after a quiet beat is drawn at once, one that follows another within the beat at the beat's
 *  end; the user's own never waits. */
export function paceWait(last: number, now = performance.now(), urgent = now - actedAt < ACTED): number {
  return urgent ? 0 : Math.max(0, last + BEAT - now);
}

/** Where each row marked data-flip="<key>" inside root is drawn now (with a move under way, where
 *  that has it), from root's own corner: scrolling, and root itself moving, are no movement.
 *  null: root is not shown (every row reads as at its corner). */
type Spots = Map<string, { x: number; y: number }>;
export function spotsOf(root: HTMLElement): Spots | null {
  if (root.offsetParent === null) return null;
  const o = root.getBoundingClientRect();
  const out: Spots = new Map();
  for (const el of root.querySelectorAll<HTMLElement>("[data-flip]")) {
    const r = el.getBoundingClientRect();
    out.set(el.dataset.flip!, { x: r.left - o.left + root.scrollLeft, y: r.top - o.top + root.scrollTop });
  }
  return out;
}

/** How a row goes from where it was (dx, dy from where it is) to its place: the reorder's time,
 *  the long move's for one going far. */
export const moveOf = (dx: number, dy: number): { frames: Keyframe[]; duration: number } => ({ frames: [{ transform: `translate(${dx}px,${dy}px)` }, { transform: "none" }], duration: motion.dur(Math.abs(dy) > 160 ? 4 : 3) });

const moving = new WeakMap<Element, Animation>();

/** One thing at a time moves what is in a container (kumo, 2026-11-21: one animation not yet
 *  done, another changed the same rows under it): whoever moves it says so, and for how long
 *  (occupy); another asks how long till it is free of the others (freeIn) and begins then, drawn
 *  meanwhile as it was (an animation's delay, filled backwards: nothing jumps to wait). who: the
 *  kind of move, which does not wait for itself: "push" the conversation's glide (chat/stick.tsx),
 *  "move" rows changing places. */
const lanes = new WeakMap<Element, Map<string, number>>();
export function occupy(el: Element, who: string, ms: number) {
  const l = lanes.get(el) ?? new Map<string, number>();
  lanes.set(el, l.set(who, performance.now() + ms));
}
export function freeIn(el: Element, who: string): number {
  let till = 0;
  for (const [k, t] of lanes.get(el) ?? []) if (k !== who) till = Math.max(till, t);
  return Math.max(0, till - performance.now());
}

/** The rows of root, drawn in their new places, moved there from where before has them. */
export function play(root: HTMLElement, before: Spots, enter: boolean) {
  if (document.hidden) return;
  const calm = reduced();
  const rows = [...root.querySelectorAll<HTMLElement>("[data-flip]")];
  // Those still moving are let go first: read where the layout has them, not where they are drawn.
  for (const el of rows) moving.get(el)?.cancel();
  const now = spotsOf(root);
  if (!now) return;
  const ease = motion.ease("out");
  // After what else moves the list (the conversation's glide): the rows stay as they were till then.
  const delay = calm ? 0 : freeIn(root, "move");
  let long = 0;
  for (const el of rows) {
    const was = before.get(el.dataset.flip!);
    const at = now.get(el.dataset.flip!)!;
    if (!was) {
      if (enter) el.animate(calm ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 0, transform: "translateY(-4px)" }, { opacity: 1, transform: "none" }], { duration: calm ? motion.dur(1) : motion.dur(2), easing: ease, delay: calm ? 0 : 40, fill: "backwards" });
      continue;
    }
    const dx = was.x - at.x;
    const dy = was.y - at.y;
    if (calm || (Math.abs(dx) < 0.5 && Math.abs(dy) < 0.5)) continue;
    const m = moveOf(dx, dy);
    moving.set(el, el.animate(m.frames, { duration: m.duration, easing: ease, delay, fill: "backwards" }));
    long = Math.max(long, m.duration);
  }
  if (long) occupy(root, "move", delay + long);
}

/** items, with those just gone kept a while where they were, marked leaving (drawn .g-depart: they
 *  fade), so that a row that goes does not vanish at once. key names an item. */
export function useLeaving<T>(items: T[], key: (x: T) => string): { item: T; leaving: boolean }[] {
  const last = useRef<{ item: T; leaving: boolean }[]>([]);
  const gone = useRef(new Map<string, number>());
  const [, tick] = useState(0);
  const now = new Set(items.map(key));
  const out: { item: T; leaving: boolean }[] = [];
  // In the order they were: those still here as they are now, the gone where they stood.
  const fresh = new Map(items.map((x) => [key(x), x]));
  for (const x of last.current) {
    const k = key(x.item);
    if (now.has(k)) out.push({ item: fresh.get(k)!, leaving: false });
    else {
      if (!gone.current.has(k)) gone.current.set(k, Date.now());
      if (Date.now() - gone.current.get(k)! < motion.dur(2)) out.push({ item: x.item, leaving: true });
    }
  }
  for (const x of items) if (!out.some((o) => !o.leaving && key(o.item) === key(x))) out.push({ item: x, leaving: false });
  for (const k of [...gone.current.keys()]) if (now.has(k) || !out.some((o) => o.leaving && key(o.item) === k)) gone.current.delete(k);
  last.current = out;
  const leaving = out.some((o) => o.leaving);
  useEffect(() => {
    if (!leaving) return;
    const t = setTimeout(() => tick((n) => n + 1), motion.dur(2) + 20);
    return () => clearTimeout(t);
  });
  return out;
}

/** A list whose items, once gone, fade where they were, then go (useLeaving, .g-depart) rather
 *  than vanish. draw: an item, and whether it is leaving. The same two boxes either way (unseen
 *  by the layout till it leaves): drawn in another kind of box to leave, the item was taken out
 *  and put in again for its own fading out. */
export function Leaving<T>({ items, keyOf, draw }: { items: T[]; keyOf: (x: T) => string; draw: (x: T, leaving: boolean) => ReactNode }) {
  return useLeaving(items, keyOf).map(({ item, leaving }) =>
    createElement("div", { key: keyOf(item), "aria-hidden": leaving || undefined, className: leaving ? "g-depart" : "contents" }, createElement("div", { className: leaving ? undefined : "contents" }, draw(item, leaving))),
  );
}
