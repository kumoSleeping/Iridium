// The script half of the motion system (motion.css has the tokens and the CSS gestures): rows that
// change places (useMoves), order that changes of itself, paced (usePaced), a sent message rising
// in, and the quick swap between two chats. All read
// their timings from the CSS tokens and move only transform and opacity; with reduced motion they
// fall back to a short fade.
import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { flushSync } from "react-dom";
import { ahead, isWhole, peekChat, since, stateAhead } from "./chats";
import { getCarried } from "./store";
import type { Turn } from "./route";
import { moveLog } from "./motionlog";

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
  dur: (n: 1 | 2 | 3 | 4) => ms(token(`--dur-${n}`, ["150ms", "220ms", "220ms", "280ms"][n - 1])),
  /** The three times (motion.css): a change of place or height; a coming or going in place; the
   *  part of a move its content fades in over (a reveal). */
  move: () => ms(token("--move", "220ms")),
  fade: () => ms(token("--fade", "150ms")),
  reveal: () => ms(token("--reveal", "110ms")),
  ease: (k: "out" | "in" | "spring") => token(`--ease-${k}`, "ease-out"),
};

// The page's one way to move what changes place (docs/动画.md). Whatever draws
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

/** Send: the new bubble fades in as the conversation is pushed up by it (the chat's glide,
 *  chat/stick.tsx), in the same move and the same time: begun together, done together. */
export function pushIn(bubble: HTMLElement, from?: number): number {
  moveLog("pushIn", "send", { ms: Math.round(motion.move()) });
  const a = reduced()
    ? bubble.animate([{ opacity: 0 }, { opacity: 1 }], { duration: motion.fade() })
    : bubble.animate([{ opacity: 0, transform: "translateY(6px) scale(0.98)" }, { opacity: 1, transform: "none" }], { duration: motion.move(), easing: motion.ease("out"), fill: "backwards" });
  // Begun at from (the timeline's time): the message's own row, come from the record in the
  // middle of its bubble's coming in, goes on from where that was, not drawn whole at once.
  const now = (document.timeline.currentTime as number | null) ?? performance.now();
  try {
    a.startTime = from ?? now;
  } catch {
    // No timeline time (a page not shown).
  }
  return from ?? now;
}
/** el fades in (motion.css --fade) as from it began (the timeline's time): drawn anew in the middle
 *  of it, it goes on from where it was; after it, nothing. */
export function fadeIn(el: HTMLElement, from: number) {
  const now = (document.timeline.currentTime as number | null) ?? performance.now();
  if (now - from >= motion.fade()) return;
  const a = el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: motion.fade(), easing: motion.ease("out"), fill: "backwards" });
  try {
    a.startTime = from;
  } catch {
    // No timeline time (a page not shown).
  }
}
/** Whether a send begun at from (pushIn's time) is still coming in. */
export const pushing = (from: number) => ((document.timeline.currentTime as number | null) ?? performance.now()) - from < motion.move();

/** How long a move to a chat waits for what it needs to be drawn whole, if that has not come yet:
 *  its first screen, in one not held whole, and its state (the composer and its keys). Its page is
 *  drawn whole as it turns, not empty and filled after. */
// Less than the time between two presses as fast as a hand makes them (kumo, 2026-11-23: rows
// pressed about 150 ms apart, each to be seen).
const WAIT_FOR_PAGE = 100;
/** Moves made this soon after the one before draw at once, whatever they have: each press is
 *  seen as it is made, filled in as its page comes, not waited for and then passed over. */
const RAPID = 400;

/** Whether the engine is WebKit (Safari, macOS WKWebView, iOS): AppleWebKit without Blink.
 *  On macOS WebKit, document.startViewTransition leaks tens of megabytes of unmapped graphics
 *  memory per call that are never reclaimed; chat switches animate the incoming log pane directly. */
export const isWebKit = (ua = typeof navigator !== "undefined" ? navigator.userAgent : "") =>
  /AppleWebKit/i.test(ua) && !/Chrome|Chromium|Edg|OPR|Android/i.test(ua);

/** Moves to a chat. Chat to chat, or from nothing open, on a computer: the main pane swaps quickly
 *  (motion.css "turn"), the old one fading out as the next fades in with a short drift, up as you go
 *  down the sidebar, down as you go up. order: the sessions as the sidebar lists them; on: whether
 *  to turn at all (not on a phone, whose pages push). Without a turn (a phone, a browser without
 *  view transitions) the chat is still drawn once it is whole, or the wait is over. Anything else
 *  is drawn at once. */
export function pageTurn(order: () => string[], on: () => boolean): Turn {
  let seq = 0;
  let turning: unknown = null;
  // When the move before was made (kumo, 2026-11-23: rows pressed fast one after another, each
  // less than the wait apart, drew none but the last).
  let lastAt = -Infinity;
  // While a turn runs the browser gives every press to the page's root (::view-transition's
  // pointer-events notwithstanding): one made then, on a row of the sidebar, did nothing (measured,
  // kumo 2026-11-23). It ends the turn and goes to what is under it.
  if (typeof document !== "undefined")
    document.addEventListener(
      "click",
      (e) => {
        const vt = turning as { skipTransition?: () => void; finished?: Promise<void> } | null;
        if (!vt || e.target !== document.documentElement || !e.isTrusted) return;
        e.stopImmediatePropagation();
        e.preventDefault();
        const { clientX: x, clientY: y } = e;
        vt.skipTransition?.();
        void (vt.finished ?? Promise.resolve()).catch(() => {}).finally(() => {
          const el = document.elementFromPoint(x, y);
          if (el && el !== document.documentElement) (el as HTMLElement).click();
        });
      },
      true,
    );
  return (from, to, draw) => {
    const doc = document as Document & { startViewTransition?: (f: () => Promise<void> | void) => { finished: Promise<void> } };
    const root = document.documentElement;
    const mine = ++seq;
    const now = performance.now();
    const rapid = now - lastAt < RAPID;
    lastAt = now;
    // Out of the settings: the chat under them is drawn already.
    if (to.name !== "chat" || (from.name !== "chat" && from.name !== "home") || (from.name === "chat" && from.id === to.id) || document.hidden) return draw();
    const still = !!document.querySelector("[data-still]");
    const webkit = isWebKit();
    const turns = on() && !still && (webkit || !!doc.startViewTransition);
    const ids = order();
    const a = from.name === "chat" ? ids.indexOf(from.id) : -1;
    const b = ids.indexOf(to.id);
    const dir = a < 0 || b < 0 || b > a ? "next" : "prev";
    const chat = peekChat(to.id);
    // One held whole, with its state: drawn now, as it was; what came since is asked for meanwhile
    // and comes in as anything new does (kumo, 2026-11-23: the swap waited a round trip for it each
    // time, nothing new or not). Only one not held, or held in part, is waited for.
    const whole = !!chat && isWhole(to.id);
    if (!chat) ahead(to.id, true);
    else since(chat, true);
    const coming = whole ? undefined : peekChat(to.id)?.coming;
    // Its state: waited for if none is held; one held from before is drawn, and freshened.
    const held = !!getCarried(`session:${to.id}`);
    const state = stateAhead(to.id, held);
    const wait = held ? [coming] : [coming, state];
    const start = () => {
      if (mine !== seq) return; // a later move draws instead
      // What still moves in the chat being left is put where it goes: the swap's picture of it is
      // as it rests, not caught halfway.
      for (const a of document.querySelector('[role="log"]')?.getAnimations({ subtree: true }) ?? []) if (a.effect?.getTiming().iterations !== Infinity) a.finish();
      // Pressed fast: drawn in the next frame, no turn. A turn's change is drawn a frame later,
      // and a press in that frame skipped it: the chat it was to draw was never seen.
      if (rapid && turning) (turning as { skipTransition?: () => void }).skipTransition?.();
      if (!turns || rapid) return draw();
      if (webkit) {
        flushSync(draw);
        const log = document.querySelector<HTMLElement>('.g-turn [role="log"]');
        const dist = parseFloat(token("--turn-in", "8px")) || 0;
        const dy = dir === "next" ? dist : -dist;
        log?.animate?.(
          reduced() || !dy ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 0, transform: `translateY(${dy}px)` }, { opacity: 1, transform: "none" }],
          { duration: ms(token("--turn-dur", "120ms")), easing: motion.ease("out") },
        );
        return;
      }
      root.dataset.turn = dir;
      const vt = doc.startViewTransition!(() => flushSync(draw));
      turning = vt;
      // By the turn itself, not by whether anything moved since: the settings opened in the
      // middle of one left the mark on, the bar and the composer drawn as for a turn.
      void vt.finished.finally(() => turning === vt && (delete root.dataset.turn, (turning = null))).catch(() => {});
    };
    if (still || rapid || !wait.some(Boolean)) return start();
    void Promise.race([Promise.all(wait), new Promise((r) => setTimeout(r, WAIT_FOR_PAGE))]).then(start);
  };
}
