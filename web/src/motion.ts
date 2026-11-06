// The script half of the motion system (motion.css has the tokens and the CSS gestures): FLIP for rows
// that move or appear, a sent message rising in, and the quick swap between two chats. All read
// their timings from the CSS tokens and move only transform and opacity; with reduced motion they
// fall back to a short fade.
import { useLayoutEffect, useRef, type RefObject } from "react";
import { flushSync } from "react-dom";
import { ahead, peekChat, since, stateAhead } from "./chats";
import { getCarried } from "./store";
import type { Turn } from "./route";

export const reduced = () => matchMedia("(prefers-reduced-motion: reduce)").matches;

function token(name: string, fallback: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}
const ms = (v: string) => (v.endsWith("ms") ? parseFloat(v) : parseFloat(v) * 1000);

export const motion = {
  dur: (n: 1 | 2 | 3 | 4) => ms(token(`--dur-${n}`, ["120ms", "200ms", "280ms", "420ms"][n - 1])),
  ease: (k: "out" | "in" | "spring") => token(`--ease-${k}`, "ease-out"),
};

/** Reorder / unfold / fold for a list: rows marked data-flip="<key>" inside ref glide from their old
 *  place to the new one; rows that are new rise in. Positions are offsets inside the container, so
 *  scrolling does not count as movement. */
export function useFlip(ref: RefObject<HTMLElement | null>, dep: unknown) {
  const last = useRef(new Map<string, number>());
  const first = useRef(true);
  useLayoutEffect(() => {
    const root = ref.current;
    if (!root) return;
    const now = new Map<string, number>();
    const rows = root.querySelectorAll<HTMLElement>("[data-flip]");
    const dur = motion.dur(3);
    const ease = motion.ease("out");
    const calm = reduced();
    rows.forEach((el) => {
      const key = el.dataset.flip!;
      const top = el.offsetTop;
      now.set(key, top);
      if (first.current) return;
      const was = last.current.get(key);
      if (was === undefined) {
        el.animate(calm ? [{ opacity: 0 }, { opacity: 1 }] : [{ opacity: 0, transform: "translateY(-4px)" }, { opacity: 1, transform: "none" }], {
          duration: calm ? motion.dur(1) : motion.dur(2),
          easing: ease,
          delay: calm ? 0 : 40,
          fill: "backwards",
        });
      } else if (was !== top && !calm) {
        el.animate([{ transform: `translateY(${was - top}px)` }, { transform: "none" }], { duration: dur, easing: ease });
      }
    });
    first.current = false;
    last.current = now;
  }, [dep, ref]);
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
    else since(chat);
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
      void vt.finished.finally(() => mine === seq && delete root.dataset.turn).catch(() => {});
    };
    if (still || !wait.some(Boolean)) return start();
    void Promise.race([Promise.all(wait), new Promise((r) => setTimeout(r, WAIT_FOR_PAGE))]).then(start);
  };
}
