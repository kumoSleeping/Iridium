// What scrolls as a chat does (Rhodium's stick.tsx): it opens at the newest, or where it was left,
// and stays at the newest as more comes; scrolled up, it brings the pages before without moving what
// you read, and what comes below shows a key to go down. At the newest, whatever comes (a message,
// a thought, a tool call, a draft's next line) pushes the conversation up, gliding, not jumping
// (the user, 2026-11-06).
import { ArrowDown } from "lucide-react";
import { useLayoutEffect, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from "react";
import { flushSync } from "react-dom";
import { t } from "../i18n";
import { motion, reduced } from "../motion";

// How near the top the page before is put in: two screens, at least 600px. It was asked for ahead
// (chats.ts), so this is drawing only.
const near = (el: HTMLElement) => Math.max(600, el.clientHeight * 2);
const END = 40; // px from the bottom that count as at the newest
const SETTLE = 600; // ms after a chat opens that what grows does not glide
const REST = 500; // ms since the user last scrolled it that a chat at its newest is at rest there
const HOLD = 350; // ms with nothing changing before the room what shrank at the newest left is closed
const SLACK = 32; // px of such room that is not worth moving the conversation for: it stays till filled
const RESIZED = 400; // ms after the pane itself changed height (the keyboard) that nothing glides

/** The glide's spring, critically damped: it comes to rest once, without a bounce, and one pushed
 *  again while it moves goes on at the speed it had. W: its stiffness, per ms. */
const springW = () => 7 / motion.dur(3);
type Spring = { y: number; v: number; t: number };
/** Where a spring let go at s is at time now, and how fast (px, px/ms). */
export function springAt(s: Spring, now: number, w: number): { y: number; v: number } {
  const dt = Math.max(0, now - s.t);
  const c = s.v + w * s.y;
  const e = Math.exp(-w * dt);
  return { y: (s.y + c * dt) * e, v: (s.v - w * c * dt) * e };
}

// The pane's bottom padding: as the page set it (its style), without a style pass of the page.
const padOf = (el: HTMLElement) => parseFloat(el.style.paddingBottom) || parseFloat(getComputedStyle(el).paddingBottom) || 0;

/** el, inside a chat, changes its own height from now for ms (an animation of its own, which says
 *  nothing as a transition does): the chat keeps its end under it, and moves nothing itself. */
const MOVES = "ir:moves";
export const layoutMoves = (el: Element, ms: number) => el.dispatchEvent(new CustomEvent(MOVES, { bubbles: true, detail: ms }));

/** Each chat's way to its newest, for what puts something at the end itself (a message sent). */
const ends = new WeakMap<HTMLElement, () => void>();
export const toEnd = (el: HTMLElement | null) => el && ends.get(el)?.();
/** Whether a chat rests at its very newest: there, and not scrolled by the user for a while (not a
 *  fling that just got there). */
const rests = new WeakMap<HTMLElement, () => boolean>();
export const restsAtEnd = (el: HTMLElement) => rests.get(el)?.() ?? false;

/** Whether a box inside the list, between it and target, takes a scroll up itself (a long output
 *  scrolled in place): the list does not move, so the newest is not let go. */
const inner = (target: EventTarget | null, list: HTMLElement) => {
  for (let n = target instanceof Element ? target : null; n && n !== list; n = n.parentElement)
    if (n.scrollTop > 0 && n.scrollHeight > n.clientHeight && /auto|scroll/.test(getComputedStyle(n).overflowY)) return true;
  return false;
};

/** quiet: no 新消息 key (an ask under it, the newest thing already in view: the key sat over it and
 *  could not be put away, the user 2026-11-08). */
/** draw: draws the page that holds the conversation again, now: the page before, once older has
 *  brought it, is put in at once and the scroll moved by as much in the same frame. Stick's own
 *  redraw draws nothing of it (its children are the page's), and drawn whenever the page next
 *  drew, the page before came in unplaced: the conversation a page off for a frame, then gliding
 *  back (as a chat opened, as a message was sent). */
export function Stick({ tail, older, draw, place, style, children, scroller, bottom, quiet }: { tail: unknown; older: () => Promise<boolean>; draw: () => void; place: { top: number | null }; style?: CSSProperties; children: ReactNode; scroller?: RefObject<HTMLDivElement | null>; bottom: number; quiet?: boolean }) {
  const own = useRef<HTMLDivElement>(null);
  const list = scroller ?? own;
  const atEnd = useRef(place.top === null);
  const drawn = useRef<unknown>(undefined);
  const paging = useRef(false);
  const [fresh, setFresh] = useState(false);
  const [, redraw] = useState(0);
  // One rule for whatever changes the conversation's height at the newest (kumo, 2026-11-17: the
  // glide, the hold and the folds each moved it their own way, against each other):
  //   - what grows there pushes the conversation up, gliding (glide);
  //   - what shrinks there moves nothing: the room it leaves is kept under the conversation (the
  //     floor) till nothing has changed for HOLD, then closed once, gliding; what grows meanwhile
  //     fills the floor first, moving nothing;
  //   - what changes its own height over time (a fold opening, moves) is drawn by itself, frame by
  //     frame: the end is only kept under it, for its part of the change.
  // The end is kept each time (pin), so no scroll the browser made of its own (a column a moment
  // shorter) is read as the user's.
  // The glide: the conversation drawn as far below its place as it was pushed, coming to it as a
  // spring does, on the compositor (not a frame drawn by script each time: the keyboard and the
  // phone's sheet keep the page busy as they move). A push while one runs adds to where that one
  // is and keeps its speed (kumo, 2026-11-19: two agents called at once, each row that came began
  // the glide again from a standstill: it went a step, stood a frame, went a step), so a draft
  // growing line by line, or several rows coming one after another, flow on as one movement.
  const anim = useRef<Animation | null>(null);
  const spring = useRef<Spring>({ y: 0, v: 0, t: 0 });
  /** When the pane itself last changed height, and its height then. */
  const paneH = useRef(0);
  const resized = useRef(-1e9);
  const colOf = (el: HTMLElement) => el.firstElementChild as HTMLElement | null;
  /** The floor: room kept under the conversation for what shrank at its end, px. */
  const spacer = useRef<HTMLDivElement>(null);
  const floor = useRef(0);
  const setFloor = (px: number) => {
    floor.current = px < 0.5 ? 0 : px;
    if (spacer.current) spacer.current.style.height = floor.current ? `${floor.current}px` : "";
  };
  /** The very end, by the layout alone: not the glide's overhang below it, which a transform adds
   *  to scrollHeight. */
  const bottomOf = (el: HTMLElement) => {
    const col = colOf(el);
    if (!col) return el.scrollHeight - el.clientHeight;
    return Math.max(0, col.offsetTop + col.offsetHeight + floor.current + padOf(el) - el.clientHeight);
  };
  // The column's height and the pane's bottom padding (the stack under it) as last measured: 0
  // till the first page is in.
  const height = useRef(0);
  const pad = useRef(0);
  const born = useRef(performance.now());
  // Where this put the scroll last, and when the user last scrolled: a chat at rest at its newest
  // may be swiped on to bring the phone's sheet (sheet.tsx).
  const ours = useRef(-1);
  const moved = useRef(0);
  /** At the very end: nothing below to scroll to, so no scroll event comes to say so. */
  const atFoot = (el: HTMLElement) => bottomOf(el) - el.scrollTop <= 1;
  /** Back at the newest: kept there again, the key to go down gone. */
  const hold = () => ((atEnd.current = true), setFresh(false));
  const pin = (el: HTMLElement) => {
    const was = el.scrollTop;
    el.scrollTop = bottomOf(el);
    ours.current = el.scrollTop;
    mark(el);
    return el.scrollTop - was;
  };
  /** To the newest, gliding up by as far as that scrolled it (a message sent). */
  const toBottom = (el: HTMLElement) => glide(pin(el));
  /** What changes its own height over time inside the conversation (a fold's track, a fold
   *  pressed, long words opened), each with its height as last read, and till when at most. */
  const moving = useRef(new Map<HTMLElement, { h: number; till: number }>());
  /** By how much those grew (or shrank) since last read; those done or gone are let go. */
  const movedBy = () => {
    let by = 0;
    const now = performance.now();
    for (const [m, r] of moving.current) {
      const h = m.isConnected ? m.offsetHeight : 0;
      by += h - r.h;
      r.h = h;
      if (!m.isConnected || now > r.till) moving.current.delete(m);
    }
    return by;
  };
  /** m's height changes of itself from now, for ms at most. What the column has changed by and
   *  was not seen yet is its already (a transition is a frame in when it says it runs). */
  const moves = (m: HTMLElement, ms: number) => {
    const el = list.current;
    if (!el || !colOf(el)?.contains(m)) return;
    const r = moving.current.get(m);
    if (r) return void (r.till = performance.now() + ms);
    moving.current.set(m, { h: m.offsetHeight, till: performance.now() + ms });
    follow(el, false, true);
  };
  const floorTimer = useRef(0);
  const settleSoon = () => {
    clearTimeout(floorTimer.current);
    floorTimer.current = window.setTimeout(settleFloor, HOLD);
  };
  /** The floor closes: nothing has changed for HOLD. Once, gliding (the user, 2026-11-08: not a
   *  bounce down and back up each time something shrinks and grows again). */
  const settleFloor = () => {
    const el = list.current;
    if (!el || !floor.current) return;
    // Not under a finger, nor while something still moves of itself: then once that is over.
    for (const [m, r] of moving.current) if (!m.isConnected || performance.now() > r.till) moving.current.delete(m);
    if (touching.current || moving.current.size) return settleSoon();
    const top = el.scrollTop;
    setFloor(0);
    // Let go of meanwhile, it may still have pulled the scroll up, at its foot: that glides too.
    if (atEnd.current) pin(el);
    else kept.current = el.scrollTop;
    glide(el.scrollTop - top);
  };
  /** Kept at the newest: what came at the end, or the stack under it grown (the composer a line
   *  taller, the phone's sheet out), pushes the conversation up by as much, gliding; a short chat
   *  that does not scroll does not move at all. The pane itself shorter or taller (the phone's
   *  keyboard) glides nothing: the conversation goes with the pane's foot as it moves.
   *  settling: a push even as the chat settles (new blocks or drafts, not a picture loading).
   *  own: all of the change is what moves of itself (moves). */
  const follow = (el: HTMLElement, settling = false, own = false) => {
    const col = colOf(el);
    const was = height.current;
    const padWas = pad.current;
    height.current = col?.offsetHeight ?? 0;
    pad.current = padOf(el);
    const self = movedBy();
    if (paneH.current && el.clientHeight !== paneH.current) resized.current = performance.now();
    paneH.current = el.clientHeight;
    if (!atEnd.current) return;
    // The pane itself made shorter or taller (the keyboard, the window): the conversation goes with
    // its foot, and what changes with it (the stack's padding as the safe area comes back under a
    // keyboard put away) glides nothing after it: one move, not there and back.
    if (performance.now() - resized.current < RESIZED) return void pin(el);
    // Not the first page coming into an empty chat, nor the page before put in (back): those are
    // drawn in place.
    const live = was > 0 && !paging.current;
    // The stack under it taller or shorter (the composer's lines), and all of the change.
    const dPad = live ? pad.current - padWas : 0;
    const d = live ? height.current - was + dPad : 0;
    // All of the column's change above what is seen (a card far up filled in late, a picture there
    // loading): what is seen from its top row to the end is as it was, so it is kept where it is,
    // the end pinned; nothing held, nothing glides (otae-c, 2026-11-19: a card at the top of a long
    // channel shrank seconds after it opened, and the end went up and then down again).
    const a = anchor.current;
    if (live && !dPad && height.current !== was && a && a.el.isConnected && col && Math.abs(lastTop.current - a.at) <= 1 && Math.abs(fromEnd(col, a.el) - a.h) < 0.5) return void pin(el);
    // The part of it that something moving of itself made: that is drawn by itself.
    const its = own ? d : d > 0 ? Math.min(d, Math.max(0, self)) : Math.max(d, Math.min(0, self));
    let push = d - its;
    // What the conversation itself shrank by is held as the floor: something in it may grow back.
    // Not as the chat opens: it is drawn whole at once. The stack made shorter is not held: the
    // conversation comes down with the composer's top at once, gliding (kumo, 2026-11-19: the
    // composer emptied left a hole over it for a moment, then the conversation dropped).
    const shrank = own ? 0 : push - dPad;
    if (shrank < -0.5 && performance.now() - born.current >= SETTLE) {
      setFloor(floor.current - shrank);
      push -= shrank;
    } else if (push > 0 && floor.current) {
      const take = Math.min(floor.current, push);
      setFloor(floor.current - take);
      push -= take;
    }
    if (floor.current > SLACK) settleSoon();
    // From where this last put it: the browser has by now pulled the scroll up itself to an end
    // made shorter, and from there nothing would be seen to move.
    const before = ours.current;
    pin(el);
    const by = before >= 0 ? el.scrollTop - before : 0;
    glide(push > 0 ? Math.min(push, Math.max(0, by)) : Math.max(push, Math.min(0, by)), settling);
  };
  /** The column's row at the top of what is seen as pin last put the end there (at: the scroll
   *  then), and how far its top is from the column's end: what follow tells a change above what is
   *  seen by. */
  const anchor = useRef<{ el: Element; h: number; at: number } | null>(null);
  const fromEnd = (col: HTMLElement, row: Element) => col.getBoundingClientRect().bottom - row.getBoundingClientRect().top;
  const mark = (el: HTMLElement) => {
    const col = colOf(el);
    anchor.current = null;
    if (!col) return;
    const top = el.getBoundingClientRect().top;
    // From the end up: the rows seen are the last few.
    for (let row = col.lastElementChild; row; row = row.previousElementSibling) {
      if (row.getBoundingClientRect().top <= top) {
        anchor.current = { el: row, h: fromEnd(col, row), at: el.scrollTop };
        return;
      }
    }
  };
  const glide = (by: number, settling = false) => {
    // A chat just opened settles in place (its pictures, the composer's cards): nothing glides. What
    // came since it was last seen, brought as it opens, still does (the user, 2026-11-06).
    if (!settling && performance.now() - born.current < SETTLE) return;
    const el = list.current;
    const col = el && colOf(el);
    if (!el || !col || !by || reduced() || document.hidden) return;
    // By the timeline's clock: where the one running is, is where the compositor draws it.
    const now = (document.timeline.currentTime as number | null) ?? performance.now();
    const w = springW();
    const cur = anim.current?.playState === "running" ? springAt(spring.current, now, w) : { y: 0, v: 0 };
    anim.current?.cancel();
    anim.current = null;
    const y = Math.max(-el.clientHeight, Math.min(el.clientHeight, cur.y + by));
    // Never past its place and back: at most as fast towards it as comes to rest there.
    const v = (cur.v + w * y) * y < 0 ? -w * y : cur.v;
    if (Math.abs(y) < 0.5) return;
    spring.current = { y, v, t: now };
    // The spring's path as linear keyframes, to rest (e^-9 of the way left).
    const T = 9 / w;
    const N = 24;
    const frames: Keyframe[] = [];
    for (let i = 0; i < N; i++) frames.push({ transform: `translateY(${springAt(spring.current, now + (T * i) / N, w).y.toFixed(2)}px)` });
    frames.push({ transform: "none" });
    const a = col.animate(frames, { duration: T, easing: "linear" });
    // Begun now, not when the page next has time for it: a page busy drawing what came (several
    // agents' rows) held each new glide at its first frame meanwhile.
    try {
      a.startTime = now;
    } catch {
      // No timeline time (a page not shown): it begins when it can.
    }
    anim.current = a;
  };

  /** Fold d opened or shut by a press (the user, 2026-11-08: clean, no jump at either end): its
   *  height glides from what it is to what it will be, its rows fading, clipped to it; shutting,
   *  it shuts once the glide is done. Nothing is drawn where it is not: what is below it moves
   *  with the layout, so nothing has to settle back at the end. */
  const toggleFold = (d: HTMLDetailsElement) => {
    if (d.dataset.anim !== undefined) return;
    const opening = !d.open;
    moves(d, motion.dur(2) + 100);
    const from = d.offsetHeight;
    if (opening) d.open = true;
    else d.dataset.shut = "";
    const sum = d.querySelector(":scope > summary") as HTMLElement | null;
    const to = opening ? d.offsetHeight : (sum?.offsetHeight ?? 0);
    const opts: KeyframeAnimationOptions = { duration: motion.dur(2), easing: motion.ease("out"), fill: "forwards" };
    d.dataset.anim = "";
    d.style.overflow = "clip";
    const fades = [...d.children].filter((c) => c !== sum).map((c) => c.animate([{ opacity: opening ? 0 : 1 }, { opacity: opening ? 1 : 0 }], opts));
    const a = d.animate([{ height: `${from}px` }, { height: `${to}px` }], opts);
    void a.finished
      .then(() => {
        if (!opening) d.open = false;
        delete d.dataset.shut;
        delete d.dataset.anim;
        d.style.overflow = "";
        a.cancel();
        for (const f of fades) f.cancel();
      })
      .catch(() => {});
  };

  /** Where the scroll was at the last scroll event; a finger on the list; a place leave scrolled
   *  to, no move of the user's. */
  const lastTop = useRef(0);
  const touching = useRef(false);
  const kept = useRef(-1);
  /** The user goes up: the newest is let go, and a glide under way stops where it is drawn, the
   *  conversation not moving on under the finger. */
  const leave = () => {
    atEnd.current = false;
    const el = list.current;
    const col = el?.firstElementChild as HTMLElement | null;
    if (!el || !col || anim.current?.playState !== "running") return;
    const tr = getComputedStyle(col).transform;
    const at = tr && tr !== "none" ? new DOMMatrixReadOnly(tr).m42 : 0;
    anim.current.cancel();
    anim.current = null;
    if (Math.abs(at) < 0.5) return;
    el.scrollTop -= at;
    kept.current = lastTop.current = el.scrollTop;
  };

  const back = async () => {
    const el = list.current;
    if (!el || paging.current) return;
    paging.current = true;
    try {
      while (el.isConnected && el.scrollTop < near(el) && (await older())) {
        const h = el.scrollHeight;
        flushSync(() => (draw(), redraw((n) => n + 1)));
        el.scrollTop += el.scrollHeight - h;
        // Placed here: what the column grew by is no push at the newest (follow).
        height.current = (el.firstElementChild as HTMLElement | null)?.offsetHeight ?? 0;
        if (atEnd.current) ours.current = el.scrollTop;
      }
    } finally {
      paging.current = false;
    }
  };

  useLayoutEffect(() => {
    const el = list.current!;
    const first = drawn.current === undefined;
    const was = drawn.current;
    drawn.current = tail;
    // Only as the conversation grew: a draw that changed nothing in it (the page around it drawn
    // again) forces no layout of the whole page; what grows without it the observer below sees.
    if (!first && was === tail) return;
    // The chat settles from its first draw, not from when it began to be made: a long one takes a
    // while to draw, and what fills in meanwhile is still its opening.
    if (first) born.current = performance.now();
    if (first && place.top !== null) el.scrollTop = place.top;
    else if (first || atEnd.current) follow(el, true);
    // Let go of at the very end (a wheel nudged up there, a box inside scrolled), and what came
    // grew nothing below it (a draft's line run on): nothing to go down to, no key that only
    // scrolling up and back down would take away (the user, 2026-11-08).
    else if (atFoot(el)) (hold(), follow(el, true));
    else setFresh(true);
    void back().catch(() => {});
  });

  // Content that grows (what the agent says, a picture loading, a fold opened at the bottom, the
  // composer growing) keeps it at the newest. Read after layout, before paint: nothing is drawn in
  // its new place first.
  useLayoutEffect(() => {
    const el = list.current!;
    const col = el.firstElementChild as HTMLElement | null;
    height.current = col?.offsetHeight ?? 0;
    pad.current = padOf(el);
    const ro = new ResizeObserver(() => follow(el));
    ro.observe(el);
    if (col) ro.observe(col);
    ends.set(el, () => ((atEnd.current = true), setFresh(false), toBottom(el)));
    rests.set(el, () => atEnd.current && bottomOf(el) - el.scrollTop < 2 && performance.now() - moved.current > REST);
    lastTop.current = el.scrollTop;
    // What says the user is going up before the scroll comes: more coming meanwhile would put it
    // back at the newest first.
    let y = 0;
    // Going down at the very end, already there: no scroll event comes, so it is held here.
    const down = () => !atEnd.current && atFoot(el) && hold();
    const wheel = (e: WheelEvent) => {
      if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
      if (e.deltaY > 0) return down();
      if (atEnd.current && !inner(e.target, el)) leave();
    };
    const touch = (e: TouchEvent) => {
      touching.current = e.type === "touchstart" || e.type === "touchmove";
      const p = e.touches[0];
      if (!p) return;
      if (e.type === "touchmove" && p.clientY - y > 2 && atEnd.current && bottomOf(el) > 0 && !inner(e.target, el)) leave();
      if (e.type === "touchmove" && y - p.clientY > 2) down();
      y = p.clientY;
    };
    const key = (e: KeyboardEvent) => {
      if ((e.target as Element).closest?.("textarea, input")) return;
      if (["PageUp", "ArrowUp", "Home"].includes(e.key)) return void (atEnd.current && leave());
      if (["PageDown", "ArrowDown", "End", " "].includes(e.key)) down();
    };
    // What changes its own height over time (moves): a fold whose height is a grid track's
    // (said.tsx Earlier, a forward's rules), opened or shut, pressed or as its turn ends; what
    // says so itself (layoutMoves). Shut at the newest it is not held as tall and settled after,
    // the answer under it going up and then down (kumo, 2026-11-16).
    const runs = (e: TransitionEvent) => (e.propertyName === "grid-template-rows" || e.propertyName === "height") && e.target instanceof HTMLElement && moves(e.target, 1000);
    const ran = (e: TransitionEvent) => {
      const r = e.target instanceof HTMLElement && moving.current.get(e.target);
      if (r) r.till = performance.now() + 60;
    };
    const told = (e: Event) => e.target instanceof HTMLElement && moves(e.target, (e as CustomEvent<number>).detail + 100);
    el.addEventListener("transitionrun", runs);
    el.addEventListener("transitionend", ran);
    el.addEventListener("transitioncancel", ran);
    el.addEventListener(MOVES, told);
    el.addEventListener("wheel", wheel, { passive: true });
    for (const k of ["touchstart", "touchmove", "touchend", "touchcancel"] as const) el.addEventListener(k, touch, { passive: true });
    el.addEventListener("keydown", key);
    return () => {
      ro.disconnect();
      clearTimeout(floorTimer.current);
      el.removeEventListener("transitionrun", runs);
      el.removeEventListener("transitionend", ran);
      el.removeEventListener("transitioncancel", ran);
      el.removeEventListener(MOVES, told);
      el.removeEventListener("wheel", wheel);
      for (const k of ["touchstart", "touchmove", "touchend", "touchcancel"] as const) el.removeEventListener(k, touch);
      el.removeEventListener("keydown", key);
      anim.current?.cancel();
      const end = bottomOf(el) - el.scrollTop < END;
      place.top = end ? null : el.scrollTop;
    };
  }, []);

  // The stack under it another height (its padding, the page's to set): followed in the same draw,
  // before it is painted. The observer sees it too, a frame after: the browser had by then pulled
  // the scroll up to the shorter end and drawn it there.
  const padNow = style?.paddingBottom;
  useLayoutEffect(() => {
    const el = list.current;
    if (el && height.current) follow(el);
  }, [padNow]);

  const scrolled = () => {
    const el = list.current!;
    const top = el.scrollTop;
    const d = top - lastTop.current;
    lastTop.current = top;
    // Where this put it is no move of the user's: the pane may have shrunk again since (the
    // keyboard coming in steps), and it is still to keep at the newest.
    if (atEnd.current && Math.abs(top - ours.current) <= 1) return;
    if (Math.abs(top - kept.current) <= 1) return void (kept.current = -1);
    moved.current = performance.now();
    // Any way up lets go of the newest at once, not only past END: a slow drag up was pulled back
    // down each time more came (the user, 2026-11-08). Only going down near it holds on again.
    const gap = bottomOf(el) - top;
    if (atEnd.current) {
      if (d < -0.5 && gap > 1) leave();
    } else if ((gap < END && d >= 0) || gap <= 1) atEnd.current = true; // at the very end too as what shrank below pulled it there
    if (atEnd.current) setFresh(false);
    // Gone up from the newest: the room kept under it is let go, unseen from here.
    else if (floor.current && gap > floor.current + 1) (clearTimeout(floorTimer.current), setFloor(0));
    void back().catch(() => {});
  };

  return (
    <>
      {/* A scrollbar that takes room (not an overlay one) has its width kept on the left too: the
          conversation stays centred in the pane, in line with the composer and the top bar under
          and over it, not off to the left by half a scrollbar (the user, 2026-11-06). */}
      <div ref={list} role="log" aria-label={t("chat.log")} onScroll={scrolled} onClickCapture={(e) => {
          const fold = (e.target as Element).closest?.("summary")?.parentElement;
          if (!(fold instanceof HTMLDetailsElement) || reduced() || document.hidden) return;
          e.preventDefault();
          toggleFold(fold);
        }} className="absolute inset-0 overflow-y-auto [overflow-anchor:none] [scrollbar-gutter:stable_both-edges]" style={style}>
        {children}
        {/* The floor: the room kept for what shrank at the end (follow). */}
        <div ref={spacer} aria-hidden />
      </div>
      {fresh && !quiet && (
        <button
          type="button"
          onClick={() => list.current!.scrollTo({ top: bottomOf(list.current!), behavior: "smooth" })}
          className="g-pop g-press glass thick float absolute left-1/2 z-10 flex h-8 -translate-x-1/2 items-center gap-1.5 rounded-full px-3 text-xs font-medium text-ink"
          style={{ bottom: bottom + 10 }}
        >
          {t("chat.newer")}
          <ArrowDown size={13} />
        </button>
      )}
    </>
  );
}
