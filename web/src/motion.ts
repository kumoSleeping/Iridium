// The script half of the motion system (motion.css has the tokens and the CSS gestures): rows that
// change places (useMoves), order that changes of itself, paced (usePaced), a sent message rising
// in, and the quick swap between two chats. All read
// their timings from the CSS tokens and move only transform and opacity; with reduced motion they
// fall back to a short fade.
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { flushSync } from "react-dom";
import { ahead, peekChat, since, stateAhead } from "./chats";
import { getCarried } from "./store";
import type { Turn } from "./route";

export const reduced = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

/** The tokens as read, kept: each read of the root's style is a style pass of the whole page,
 *  forced in the middle of whatever is moving (a page turned in the phone's sheet stuttered). Read
 *  again only as reduced motion is turned on or off, which changes them (motion.css). */
const tokens = new Map<string, string>();
if (typeof window !== "undefined") matchMedia("(prefers-reduced-motion: reduce)").addEventListener?.("change", () => tokens.clear());
function token(name: string, fallback: string) {
  let v = tokens.get(name);
  if (v === undefined) {
    v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    if (v) tokens.set(name, v);
  }
  return v || fallback;
}
const ms = (v: string) => (v.endsWith("ms") ? parseFloat(v) : parseFloat(v) * 1000);

export const motion = {
  dur: (n: 1 | 2 | 3 | 4) => ms(token(`--dur-${n}`, ["120ms", "200ms", "280ms", "420ms"][n - 1])),
  ease: (k: "out" | "in" | "spring") => token(`--ease-${k}`, "ease-out"),
};

// The page's one way to move what changes place (docs/前端设计规范.md "动画协调"). Whatever draws
// a list, the sidebar's rows or a conversation's turns, asks here; none animates a move of its own.

/** Rows that change places (reorder, unfold, fold, one put between others): each marked
 *  data-flip="<key>" inside ref glides from where it was drawn to where it now is, those that
 *  swap passing each other, instead of all jumping at once. The returned function is called as
 *  the list is drawn, with what its order is (a string): when that differs from the last drawn,
 *  the rows are read where they are, before the change, and moved from there once it is drawn.
 *  One already moving goes on from where it is. Draws that change no order cost nothing: what
 *  only grows (a draft's next line) is not this one's to move. scope: which list this is, when
 *  one place draws several in turn (a chat, then the next): none moves from where another's rows
 *  were. enter: rows new to the list fade in. Only transform and opacity; with reduced motion
 *  nothing glides. All of it is moves.ts, brought once the page has settled (not in the first
 *  screen, whose size is kept: nothing changes places as it is first drawn). */
let moves: typeof import("./moves") | undefined;
export const movesAhead = () => void import("./moves").then((m) => (moves = m));
export function useMoves(ref: RefObject<HTMLElement | null>, enter = false) {
  const st = useRef({});
  useLayoutEffect(() => moves?.drawn(st.current, ref.current, enter));
  return (order: string, scope?: string) => moves?.draws(st.current, ref.current, order, scope);
}

/** A value whose order changes of itself (the sidebar's rows as sessions work), paced: order is
 *  what its order is, as a string. While that is the same, the value is drawn as it comes (what a
 *  row says is never late). A change of order is drawn at once when none was drawn in the last
 *  beat, else at the beat's end, the latest then (those between are never drawn): a list several
 *  sessions keep reordering moves once a beat, not at each one's every step. What the user did
 *  (a row dragged, a session made) never waits (moves.ts paceWait). */
export function usePaced<T>(value: T, order: string): T {
  const held = useRef({ value, order, at: 0 });
  const [, tick] = useState({});
  const h = held.current;
  let wait = 0;
  if (order === h.order) h.value = value;
  else if (!(wait = moves?.paceWait(h.at) ?? 0)) held.current = { value, order, at: performance.now() };
  useEffect(() => {
    const t = wait && setTimeout(tick, wait, {});
    return () => clearTimeout(t);
  });
  return held.current.value;
}

/** Glide: a row whose keys change width (a key gains an icon, a name grows, a key comes or goes):
 *  the others slide to where they now are instead of jumping, a new one fades in. Watches the row
 *  itself, so it holds whichever component re-rendered. Read after layout, before paint (the
 *  observers' callbacks), so nothing is drawn in the wrong place first. */
export function useGlide(ref: RefObject<HTMLElement | null>) {
  useLayoutEffect(() => {
    const root = ref.current;
    if (!root) return;
    const read = () => new Map([...root.children].map((el) => [el as HTMLElement, (el as HTMLElement).offsetLeft]));
    let last = read();
    const check = () => {
      const now = read();
      const calm = reduced();
      for (const [el, x] of now) {
        const was = last.get(el);
        if (was === undefined) el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: motion.dur(2), easing: motion.ease("out") });
        else if (was !== x && !calm) el.animate([{ transform: `translateX(${was - x}px)` }, { transform: "none" }], { duration: motion.dur(3), easing: motion.ease("out") });
      }
      last = now;
    };
    const ro = new ResizeObserver(check);
    const watch = () => {
      ro.disconnect();
      ro.observe(root);
      for (const el of root.children) ro.observe(el);
    };
    watch();
    const mo = new MutationObserver(() => (watch(), check()));
    mo.observe(root, { childList: true });
    return () => (ro.disconnect(), mo.disconnect());
  }, [ref]);
}

/** Send: the new bubble fades in as it comes up; the conversation pushed up by it, as a chat app
 *  does, is the chat's glide (chat/stick.tsx), as for anything else that comes at the end. */
export function pushIn(bubble: HTMLElement) {
  if (reduced()) {
    bubble.animate([{ opacity: 0 }, { opacity: 1 }], { duration: motion.dur(1) });
    return;
  }
  bubble.animate([{ opacity: 0, transform: "translateY(6px) scale(0.98)" }, { opacity: 1, transform: "none" }], { duration: motion.dur(3), easing: motion.ease("out"), delay: motion.dur(1) / 2, fill: "backwards" });
}

/** How long a move to a chat waits for what it needs to be drawn whole, if that has not come yet:
 *  its newest page (or what came since, in one held) and its state (the composer and its keys).
 *  Its page is drawn whole as it turns, not empty and filled after. */
const WAIT_FOR_PAGE = 160;

/** Moves to a chat. Chat to chat, or from nothing open, on a computer: the main pane swaps quickly
 *  (motion.css "turn"), the old one fading out as the next fades in with a short drift, up as you go
 *  down the sidebar, down as you go up. order: the sessions as the sidebar lists them; on: whether
 *  to turn at all (not on a phone, whose pages push). Without a turn (a phone, a browser without
 *  view transitions) the chat is still drawn once it is whole, or the wait is over. Anything else
 *  is drawn at once. */
export function pageTurn(order: () => string[], on: () => boolean): Turn {
  let seq = 0;
  let turning: unknown = null;
  return (from, to, draw) => {
    const doc = document as Document & { startViewTransition?: (f: () => Promise<void> | void) => { finished: Promise<void> } };
    const root = document.documentElement;
    const mine = ++seq;
    // Out of the settings: the chat under them is drawn already.
    if (to.name !== "chat" || (from.name !== "chat" && from.name !== "home") || (from.name === "chat" && from.id === to.id) || document.hidden) return draw();
    const still = !!document.querySelector("[data-still]");
    const turns = on() && !!doc.startViewTransition && !still;
    const ids = order();
    const a = from.name === "chat" ? ids.indexOf(from.id) : -1;
    const b = ids.indexOf(to.id);
    const dir = a < 0 || b < 0 || b > a ? "next" : "prev";
    const chat = peekChat(to.id);
    // One held: what came since it was seen comes first, so it turns in as it is now.
    if (!chat) ahead(to.id, true);
    else since(chat, true);
    const coming = peekChat(to.id)?.coming;
    // Its state: waited for if none is held; one held from before is drawn, and freshened.
    const held = !!getCarried(`session:${to.id}`);
    const state = stateAhead(to.id, held);
    const wait = held ? [coming] : [coming, state];
    const start = () => {
      if (mine !== seq) return; // a later move draws instead
      if (!turns) return draw();
      root.dataset.turn = dir;
      const vt = doc.startViewTransition!(() => flushSync(draw));
      turning = vt;
      // By the turn itself, not by whether anything moved since: the settings opened in the
      // middle of one left the mark on, the bar and the composer drawn as for a turn.
      void vt.finished.finally(() => turning === vt && (delete root.dataset.turn, (turning = null))).catch(() => {});
    };
    if (still || !wait.some(Boolean)) return start();
    void Promise.race([Promise.all(wait), new Promise((r) => setTimeout(r, WAIT_FOR_PAGE))]).then(start);
  };
}
