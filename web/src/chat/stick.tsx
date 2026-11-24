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
import { moveLog } from "../motionlog";

// How near the top the page before is put in: two screens, at least 600px. It was asked for ahead
// (chats.ts), so this is drawing only.
/** held: what comes is drawn from what the page holds already (turns not drawn yet), quick: one
 *  screen is room enough, and less is kept drawn. */
const near = (el: HTMLElement, held = false) => (held ? Math.max(500, el.clientHeight) : Math.max(600, el.clientHeight * 2));
const END = 40; // px from the bottom that count as at the newest
const SETTLE = 600; // ms after a chat opens that what fills in is put in place at once, not followed
const REST = 500; // ms since the user last scrolled it that a chat at its newest is at rest there
const SLACK = 32; // px of room not worth bringing the conversation down for: left till what grows next fills it, or let go unseen as the user goes up
const QUIET = 250; // ms with nothing changing, the turn over, before the room kept under the conversation is closed: a turn ends in several draws (its box gone, its answer come, its steps folded), and the room is closed for all of them once

/** The follow's spring, critically damped: it comes to rest once, without a bounce, and one whose
 *  end moves on while it runs goes on at the speed it had. W: its stiffness, per ms: at rest in
 *  one move's time (motion.css --move), softening the landing without dragging. */
const springW = (quick = true) => 6.2 / (quick ? motion.move() : motion.move() * FOLLOW);
/** How many moves' time what comes of itself takes to come to rest (the agent's lines, a row, a
 *  picture): slower than the lines come, so the conversation never stands between them: one
 *  movement, its speed kept (kumo, 2026-11-23). What the user does (a message sent, the composer's
 *  height) is a move as any, in one move's time. */
const FOLLOW = 3;
type Spring = { y: number; v: number; t: number; w?: number };
/** Where a spring let go at s is at time now, and how fast (px, px/ms). */
export function springAt(s: Spring, now: number, w: number): { y: number; v: number } {
  const dt = Math.max(0, now - s.t);
  const c = s.v + w * s.y;
  const e = Math.exp(-w * dt);
  return { y: (s.y + c * dt) * e, v: (s.v - w * c * dt) * e };
}

/** What an element is, for the record: its row's key and its own class's first words. */
const nameOf = (el: HTMLElement) => `${el.closest<HTMLElement>("[data-flip]")?.dataset.flip?.slice(0, 16) ?? ""} ${el.tagName.toLowerCase()}.${String(el.className).split(" ").slice(0, 2).join(".")}`;
// The pane's bottom padding: as the page set it (its style), without a style pass of the page.
const padOf = (el: HTMLElement) => parseFloat(el.style.paddingBottom) || parseFloat(getComputedStyle(el).paddingBottom) || 0;

/** el, inside a chat, changes its own height from now for ms (an animation of its own, which says
 *  nothing as a transition does): the chat keeps its end under it, and moves nothing itself. */
const MOVES = "ir:moves";
export const layoutMoves = (el: Element, ms: number) => el.dispatchEvent(new CustomEvent(MOVES, { bubbles: true, detail: ms }));

/** Each chat's way to its newest, for what puts something at the end itself (a message sent). */
const ends = new WeakMap<HTMLElement, () => void>();
export const toEnd = (el: HTMLElement | null) => el && ends.get(el)?.();
/** Each chat's way to let go of its newest, for what scrolls it up itself (a quoted message gone
 *  to): what comes meanwhile does not pull it back down. */
const goes = new WeakMap<HTMLElement, () => void>();
export const letGo = (el: HTMLElement | null) => el && goes.get(el)?.();
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
 *  brought it, is put in at once and the scroll moved by as much in the same frame. */
/** held: turns the page holds and has not drawn are over what is drawn (older draws them). */
/** busy: a turn is on (the chat's own agent's, another's): what shrinks meanwhile moves nothing. */
export function Stick({ tail, older, draw, place, style, children, scroller, bottom, quiet, held, busy }: { tail: unknown; older: () => Promise<boolean>; draw: () => void; place: { top: number | null }; style?: CSSProperties; children: ReactNode; scroller?: RefObject<HTMLDivElement | null>; bottom: number; quiet?: boolean; held?: boolean; busy?: boolean }) {
  const heldNow = useRef(held);
  heldNow.current = held;
  const busyNow = useRef(busy);
  busyNow.current = busy;
  const own = useRef<HTMLDivElement>(null);
  const list = scroller ?? own;
  const atEnd = useRef(place.top === null);
  const drawn = useRef<unknown>(undefined);
  const paging = useRef(false);
  const [fresh, setFresh] = useState(false);
  const [, redraw] = useState(0);
  // One rule for the conversation at its newest (docs/动画.md): the layout is always as it will
  // stay, and the only thing that moves the conversation is its scroll place going to its end.
  //   - what grows at the end is scrolled into view, the scroll place on a spring to the end:
  //     it only ever goes towards the end, so the conversation only ever goes up for it;
  //   - what shrinks moves nothing: as much room is kept under the conversation (the room),
  //     what grows next fills that first, and what is left is closed once, the conversation
  //     coming down in one move, when the turn is over and nothing has changed for QUIET, or at
  //     once for what the user did (the composer made shorter);
  //   - what changes over what is seen (a card far up filled in, a picture there) moves nothing
  //     seen: the scroll place goes with it at once;
  //   - what changes its own height over time (a fold's track, a fold pressed, long words) is its
  //     own to draw: the end is kept under it, frame by frame;
  // No row is moved for any of it (no transform): rows that change places (moves.ts) are another
  // thing, on another property, and neither waits for the other.
  const colOf = (el: HTMLElement) => el.firstElementChild as HTMLElement | null;
  /** The room kept under the conversation, px, and where it is going (0 as it closes). */
  const spacer = useRef<HTMLDivElement>(null);
  const room = useRef(0);
  const setRoom = (px: number) => {
    room.current = px < 0.5 ? 0 : px;
    if (spacer.current) spacer.current.style.height = room.current ? `${room.current}px` : "";
  };
  /** The column's height, to the part of a pixel: a row shutting by an animation is a part of
   *  one shorter each frame, and by whole ones the end was a pixel or two off, there and back. */
  const tall = (col: HTMLElement | null) => col?.getBoundingClientRect().height ?? 0;
  /** The very end, by the layout. */
  const bottomOf = (el: HTMLElement) => {
    const col = colOf(el);
    if (!col) return el.scrollHeight - el.clientHeight;
    return Math.max(0, col.offsetTop + tall(col) + room.current + padOf(el) - el.clientHeight);
  };
  // The column's height, the pane's bottom padding and height as last seen: 0 till the first page is in.
  const height = useRef(0);
  const pad = useRef(0);
  const paneH = useRef(0);
  const born = useRef(performance.now());
  // Where this put the scroll last, and when the user last scrolled: a chat at rest at its newest
  // may be swiped on to bring the phone's sheet (sheet.tsx).
  const ours = useRef(-1);
  const moved = useRef(0);
  /** At the very end: nothing below to scroll to, so no scroll event comes to say so. */
  const atFoot = (el: HTMLElement) => bottomOf(el) - el.scrollTop <= 1;
  /** Back at the newest: kept there again, the key to go down gone. */
  const hold = () => ((atEnd.current = true), setFresh(false));
  /** The row at the top of what is seen, and where the layout has it: what changes over it is
   *  told from what changes at the end by it. */
  const anchor = useRef<{ el: HTMLElement; top: number } | null>(null);
  const mark = (el: HTMLElement) => {
    const col = colOf(el);
    anchor.current = null;
    if (!col) return;
    const top = el.getBoundingClientRect().top;
    // From the end up: the rows seen are the last few.
    for (let row = col.lastElementChild as HTMLElement | null; row; row = row.previousElementSibling as HTMLElement | null) {
      if (row.getBoundingClientRect().top <= top) return void (anchor.current = { el: row, top: row.offsetTop });
    }
    const first = col.firstElementChild as HTMLElement | null;
    if (first) anchor.current = { el: first, top: first.offsetTop };
  };
  // The follow: the scroll place (at) on its way to the end, and the room on its way shut, each a
  // spring, drawn a frame at a time while either has somewhere to go.
  const at = useRef({ y: 0, v: 0 });
  const quick = useRef(false);
  const closing = useRef<{ v: number; quick: boolean } | null>(null);
  const frame = useRef(0);
  const last = useRef(0);
  const put = (el: HTMLElement, top: number) => {
    el.scrollTop = top;
    ours.current = el.scrollTop;
  };
  /** To the end at once: no follow under way. */
  const pin = (el: HTMLElement) => {
    at.current = { y: 0, v: 0 };
    put(el, bottomOf(el));
    mark(el);
  };
  const step = (now: number) => {
    frame.current = 0;
    const el = list.current;
    if (!el || !atEnd.current) return;
    const dt = Math.min(64, Math.max(1, now - last.current));
    last.current = now;
    let more = false;
    // What changed height since the last frame is taken in first (the observer tells of it only
    // after this): put by the new end before that, the scroll place went back by as much as
    // something had shrunk, and the conversation down with it for a frame.
    const col = colOf(el);
    if (col && (Math.abs(tall(col) - height.current) > 0.01 || padOf(el) !== pad.current || el.clientHeight !== paneH.current)) {
      stepping.current = true;
      follow(el, "?", performance.now() - actedAt.current < USER);
      stepping.current = false;
    }
    // The room shutting: the conversation comes down with it, the end kept.
    const c = closing.current;
    if (c && room.current) {
      const s = springAt({ y: room.current, v: c.v, t: 0 }, dt, springW(c.quick));
      c.v = s.v;
      setRoom(s.y < 0.5 ? 0 : s.y);
      if (room.current) more = true;
      else closing.current = null;
      put(el, bottomOf(el) - at.current.y);
    } else closing.current = null;
    // The scroll place going to the end: y, how far it has yet to go.
    if (at.current.y > 0) {
      const s = springAt({ y: at.current.y, v: at.current.v, t: 0 }, dt, springW(quick.current));
      at.current = s.y < 0.5 ? { y: 0, v: 0 } : { y: s.y, v: Math.min(0, s.v) };
      put(el, bottomOf(el) - at.current.y);
      if (at.current.y) more = true;
    }
    if (more) frame.current = requestAnimationFrame(step);
    else mark(el);
  };
  const stepping = useRef(false);
  /** When the user last did something (a press, a key): what changes height right after is
   *  theirs, in one move's time, and what shrank comes down at once. USER: as long as a fold
   *  pressed takes to open or shut (motion.css --move and --fade), and a frame or two. */
  const actedAt = useRef(-1e9);
  const USER = 500;
  /** Each row's height as last seen, for the record: which row it was that changed with no one
   *  saying why. */
  const rowsH = useRef(new Map<Element, number>());
  const whichRows = (col: HTMLElement | null) => {
    const out: string[] = [];
    const now = new Map<Element, number>();
    for (const r of col?.children ?? []) {
      const h = (r as HTMLElement).offsetHeight;
      now.set(r, h);
      const was = rowsH.current.get(r);
      if (was === undefined) out.push(`+${(r as HTMLElement).dataset.flip?.slice(0, 14) ?? "row"}:${h}`);
      else if (was !== h) out.push(`${(r as HTMLElement).dataset.flip?.slice(0, 14) ?? "row"}:${h - was > 0 ? "+" : ""}${h - was}`);
    }
    for (const [r, h] of rowsH.current) if (!now.has(r)) out.push(`-${(r as HTMLElement).dataset.flip?.slice(0, 14) ?? "row"}:${h}`);
    rowsH.current = now;
    return out.slice(0, 4).join(" ");
  };
  const run = () => {
    if (frame.current || stepping.current) return;
    last.current = performance.now();
    frame.current = requestAnimationFrame(step);
  };
  /** The room shuts from now. */
  const close = (isQuick: boolean) => {
    if (!room.current) return;
    if (reduced() || document.hidden) return void (setRoom(0), list.current && atEnd.current && pin(list.current));
    moveLog("follow.close", whyNow.current, { dy: Math.round(room.current), note: isQuick ? "quick" : "" });
    closing.current = { v: closing.current?.v ?? 0, quick: isQuick || !!closing.current?.quick };
    run();
  };
  /** Why the conversation is being followed now, for the record (motionlog.ts). */
  const whyNow = useRef("?");
  const quietTimer = useRef(0);
  const closeSoon = () => {
    clearTimeout(quietTimer.current);
    if (room.current > SLACK) quietTimer.current = window.setTimeout(() => (busyNow.current ? closeSoon() : ((whyNow.current = "rest"), close(false))), QUIET);
  };
  /** What changes its own height over time inside the conversation (a fold's track opening or
   *  shutting, a fold pressed, long words opened), each with its height as last read, and till
   *  when at most: its change is drawn by itself, frame by frame, and the end is only kept under
   *  it (neither room kept for it nor followed after it: what is under a fold shutting went up
   *  with it and came down again once the room was closed). When a fold collapses while detached
   *  but closer to the bottom than its own height (0 < gap < h0), blend interpolates scrollTop
   *  proportionally across the whole collapse so it never pulls from below and then hits the
   *  bottom floor halfway through (kumo, 2026-11-24). */
  const moving = useRef(new Map<HTMLElement, { h: number; till: number; blend?: { h0: number; span: number; startTop: number; deficit: number } }>());
  const clearBlend = () => {
    for (const r of moving.current.values()) r.blend = undefined;
  };
  const hasBlend = () => {
    for (const r of moving.current.values()) if (r.blend) return true;
    return false;
  };
  const blendMove = (el: HTMLElement) => {
    for (const [m, r] of moving.current) {
      const b = r.blend;
      if (!b) continue;
      if (!m.isConnected) {
        r.blend = undefined;
        continue;
      }
      const h = m.getBoundingClientRect().height;
      // Re-opened mid-collapse: stop blending and expand downward normally.
      if (h > r.h + 0.5) {
        r.blend = undefined;
        continue;
      }
      const p = Math.min(1, Math.max(0, (b.h0 - h) / b.span));
      const end = bottomOf(el);
      const target = Math.max(0, Math.min(end, b.startTop - b.deficit * p));
      put(el, target);
      lastTop.current = el.scrollTop;
      if (p >= 0.999) r.blend = undefined;
    }
  };
  /** By how much those grew (or shrank) since last read; those done or gone are let go. */
  const movedBy = () => {
    let by = 0;
    const now = performance.now();
    for (const [m, r] of moving.current) {
      const h = m.isConnected ? m.getBoundingClientRect().height : 0;
      by += h - r.h;
      r.h = h;
      if (!m.isConnected || now > r.till) moving.current.delete(m);
    }
    return by;
  };
  /** m's height changes of itself from now, for ms at most. */
  const moves = (m: HTMLElement, ms: number, fresh = false, targetH = 0) => {
    const el = list.current;
    if (!el || !colOf(el)?.contains(m)) return;
    const h0 = m.getBoundingClientRect().height;
    const span = Math.max(0, h0 - targetH);
    const gap0 = Math.max(0, bottomOf(el) - el.scrollTop);
    const blend = !atEnd.current && span > 1 && gap0 > 1 && gap0 < span ? { h0, span, startTop: el.scrollTop, deficit: span - gap0 } : undefined;
    const r = moving.current.get(m);
    if (r) {
      r.till = performance.now() + ms;
      if (fresh) {
        r.h = h0;
        r.blend = blend;
      }
      return;
    }
    // What the column has changed by and was not seen yet is its already (a transition is a frame
    // in when it says it runs).
    moving.current.set(m, { h: h0, till: performance.now() + ms, blend });
    const col = colOf(el);
    const d = tall(col) - height.current;
    if (Math.abs(d) > 0.01) (moving.current.get(m)!.h -= d), follow(el, "own-height");
  };
  /** The conversation or the stack under it changed height, the chat at its newest. why: what
   *  for; user: the user's own doing (one move's time, and what shrank comes down at once). */
  const follow = (el: HTMLElement, why: string, user = false) => {
    const col = colOf(el);
    const was = height.current;
    const padWas = pad.current;
    const paneWas = paneH.current;
    height.current = tall(col);
    pad.current = padOf(el);
    paneH.current = el.clientHeight;
    whyNow.current = why;
    // Keep tracked own-height elements in sync with height.current while detached, so re-attaching
    // mid- or post-transition never dumps a stale delta into follow() as a fake shrink/grow bounce.
    if (!atEnd.current) {
      blendMove(el);
      movedBy();
      return;
    }
    // The first page into an empty chat, the page before put in, the pane itself another height
    // (the keyboard, the window), a chat still settling as it opens, no motion wanted: in place.
    const settling = performance.now() - born.current < SETTLE && why !== "tail";
    if (!was || paging.current || (paneWas && paneWas !== paneH.current) || settling || reduced() || document.hidden) {
      if (!was || paging.current) setRoom(0);
      return void pin(el);
    }
    const dPad = pad.current - padWas;
    let d = height.current - was;
    // What changes its own height: the end kept under it, at once.
    const self = movedBy();
    d -= self;
    // What changed over what is seen moves nothing seen: the scroll place goes with it.
    const a = anchor.current;
    const over = a && a.el.isConnected ? a.el.offsetTop - a.top : 0;
    if (a && over) a.top += over;
    d -= over;
    let grow = Math.max(0, d) + Math.max(0, dPad);
    const shrink = Math.max(0, -d) + Math.max(0, -dPad);
    // What shrank: as much room kept (nothing moves).
    if (shrink) setRoom(room.current + shrink);
    // What grew fills the room first.
    if (grow && room.current) {
      const take = Math.min(room.current, grow);
      setRoom(room.current - take);
      grow -= take;
    }
    // And what the scroll place has yet to go is as much less as there is room left: one way only.
    if (room.current && at.current.y > 0) {
      const take = Math.min(room.current, at.current.y);
      setRoom(room.current - take);
      at.current.y -= take;
    }
    // From where this last put it: the browser may have pulled the scroll up to a shorter end.
    const end = bottomOf(el);
    const from = Math.min(end, Math.max(0, (ours.current >= 0 ? ours.current : el.scrollTop) + over + Math.max(0, self)));
    at.current.y = Math.max(0, end - from);
    // More came at once than the pane shows (a chat opened with many new messages, an answer come
    // whole): not followed, which ran the conversation up past what no one had read (kumo,
    // 2026-11-24: it shot up, twitching, as each lot came). It stays where it is, what came drawn
    // under it, and the key to go down shows; what comes after is under it too. Not what the user
    // did (a message sent goes to its end).
    const seen = el.clientHeight - (parseFloat(el.style.paddingTop) || 0) - pad.current;
    // By what came now, or by all that is yet to go when that is not the user's own way to the
    // end (a message sent, still on its way as its block comes).
    const far = Math.max(200, seen);
    if (!user && (grow > far || (at.current.y > far && !quick.current))) {
      moveLog("follow.stay", why, { dy: 0, note: `${Math.round(at.current.y)}px came, ${Math.round(seen)} seen` });
      put(el, from);
      leave();
      setRoom(0);
      setFresh(true);
      return;
    }
    if (grow > 0.5) quick.current = user;
    put(el, from);
    if (at.current.y > 0.5) (moveLog("follow.up", why, { dy: -Math.round(at.current.y), note: user ? "quick" : "" }), run());
    else pin(el);
    const rows = d || over || self ? whichRows(col) : "";
    if (Math.abs(self) > 0.5) moveLog("follow.own", why, { dy: Math.round(self) });
    if (shrink) moveLog("follow.room", why, { dy: Math.round(shrink), note: `room ${Math.round(room.current)} ${rows}` });
    else if (why === "?" && (d || over)) moveLog("follow.grew", why, { dy: Math.round(d), note: `${over ? `over ${over} ` : ""}${rows}` });
    if (room.current) user && shrink ? close(true) : closeSoon();
    if (Math.abs(over) > 0.5) moveLog("follow.over", why, { dy: Math.round(over) });
  };

  /** Fold d opened or shut by a press (the user, 2026-11-08: clean, no jump at either end): its
   *  height glides from what it is to what it will be, its rows fading, clipped to it; shutting,
   *  it shuts once the glide is done. Nothing is drawn where it is not: what is below it moves
   *  with the layout, so nothing has to settle back at the end. */
  const toggleFold = (d: HTMLDetailsElement) => {
    if (d.dataset.anim !== undefined) return;
    const opening = !d.open;
    const sum = d.querySelector(":scope > summary") as HTMLElement | null;
    moveLog("stick.fold", opening ? "fold-open" : "fold-shut");
    moves(d, motion.move() + motion.fade() + 100, true, opening ? 0 : (sum?.offsetHeight ?? 0));
    const from = d.offsetHeight;
    if (opening) d.open = true;
    else d.dataset.shut = "";
    const to = opening ? d.offsetHeight : (sum?.offsetHeight ?? 0);
    // As a reveal (motion.css g-reveal): opening, the room first, its rows fading in over its last
    // part; shutting, the rows fading out first, then the room.
    const ease = motion.ease("out");
    const move = motion.move();
    const fade = motion.fade();
    const lag = opening ? move - motion.reveal() : fade - motion.reveal();
    d.dataset.anim = "";
    d.style.overflow = "clip";
    const fades = [...d.children].filter((c) => c !== sum).map((c) => c.animate([{ opacity: opening ? 0 : 1 }, { opacity: opening ? 1 : 0 }], { duration: fade, easing: ease, delay: opening ? lag : 0, fill: "both" }));
    const a = d.animate([{ height: `${from}px` }, { height: `${to}px` }], { duration: move, easing: ease, delay: opening ? 0 : lag, fill: "both" });
    void Promise.all([a.finished, ...fades.map((f) => f.finished)])
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

  /** Where the scroll was at the last scroll event. */
  const lastTop = useRef(0);
  /** The user goes up: the newest is let go, and a follow under way stops where it is. */
  const leave = () => {
    atEnd.current = false;
    cancelAnimationFrame(frame.current);
    frame.current = 0;
    at.current = { y: 0, v: 0 };
    closing.current = null;
    clearTimeout(quietTimer.current);
  };

  /** The key to go down, pressed: at the newest at once, as a chat app goes (kumo, 2026-11-24: the
   *  browser's smooth scroll over a long way was slow, and slower as it went): put a little
   *  short of the end, the last of the way in one move's time. */
  const NEAR = 96;
  const goDown = () => {
    const el = list.current;
    if (!el) return;
    hold();
    setRoom(0);
    const end = bottomOf(el);
    if (reduced() || document.hidden) return void pin(el);
    const from = Math.max(el.scrollTop, end - NEAR);
    put(el, from);
    at.current = { y: end - from, v: 0 };
    quick.current = true;
    moveLog("follow.up", "key", { dy: -Math.round(end - from), note: "quick" });
    if (at.current.y > 0.5) run();
    else pin(el);
  };

  const back = async () => {
    const el = list.current;
    if (!el || paging.current) return;
    paging.current = true;
    try {
      while (el.isConnected && el.scrollTop < near(el, heldNow.current) && (await older())) {
        const h = el.scrollHeight;
        flushSync(() => (draw(), redraw((n) => n + 1)));
        const by = el.scrollHeight - h;
        el.scrollTop += by;
        moveLog("stick.back", "page", { dy: 0, note: `+${by}px over` });
        // Placed here: what the column grew by is nothing to follow.
        height.current = tall(colOf(el));
        if (atEnd.current) ours.current = el.scrollTop;
        mark(el);
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
    else if (first || atEnd.current) follow(el, first ? "open" : "tail");
    // Let go of at the very end (a wheel nudged up there, a box inside scrolled), and what came
    // grew nothing below it (a draft's line run on): nothing to go down to, no key that only
    // scrolling up and back down would take away (the user, 2026-11-08).
    else if (atFoot(el)) (hold(), follow(el, "tail"));
    else setFresh(true);
    void back().catch(() => {});
  });

  // Content that grows (what the agent says, a picture loading, a fold opened at the bottom, the
  // composer growing) keeps it at the newest. Read after layout, before paint: nothing is drawn in
  // its new place first.
  useLayoutEffect(() => {
    const el = list.current!;
    const col = colOf(el);
    height.current = tall(col);
    pad.current = padOf(el);
    paneH.current = el.clientHeight;
    const act = () => (actedAt.current = performance.now());
    const ro = new ResizeObserver(() => {
      const user = performance.now() - actedAt.current < USER;
      follow(el, user ? "user" : "?", user);
    });
    ro.observe(el);
    if (col) ro.observe(col);
    ends.set(el, () => ((atEnd.current = true), setFresh(false), (whyNow.current = "send"), close(true), follow(el, "send", true)));
    goes.set(el, leave);
    rests.set(el, () => atEnd.current && bottomOf(el) - el.scrollTop < 2 && performance.now() - moved.current > REST);
    lastTop.current = el.scrollTop;
    // What says the user is going up before the scroll comes: more coming meanwhile would put it
    // back at the newest first.
    let y = 0;
    // Going down at the very end, already there: no scroll event comes, so it is held here.
    const down = () => !atEnd.current && atFoot(el) && hold();
    const wheel = (e: WheelEvent) => {
      if (Math.abs(e.deltaY) <= Math.abs(e.deltaX)) return;
      clearBlend();
      if (e.deltaY > 0) return down();
      if (atEnd.current && !inner(e.target, el)) leave();
    };
    const touch = (e: TouchEvent) => {
      const p = e.touches[0];
      if (!p) return;
      if (e.type === "touchmove" && Math.abs(p.clientY - y) > 2) clearBlend();
      if (e.type === "touchmove" && p.clientY - y > 2 && atEnd.current && bottomOf(el) > 0 && !inner(e.target, el)) leave();
      if (e.type === "touchmove" && y - p.clientY > 2) down();
      y = p.clientY;
    };
    const key = (e: KeyboardEvent) => {
      if ((e.target as Element).closest?.("textarea, input")) return;
      if (["PageUp", "ArrowUp", "Home"].includes(e.key)) return void (clearBlend(), atEnd.current && leave());
      if (["PageDown", "ArrowDown", "End", " "].includes(e.key)) (clearBlend(), down());
    };
    // For the record (motionlog.ts): what in the conversation changes its own height or moves by
    // an animation of its own.
    const runs = (e: TransitionEvent) => (e.propertyName === "grid-template-rows" || e.propertyName === "height") && e.target instanceof HTMLElement && (moveLog("css.transition", e.propertyName, { note: nameOf(e.target) }), moves(e.target, 1000));
    const ran = (e: TransitionEvent) => {
      const r = e.target instanceof HTMLElement && moving.current.get(e.target);
      if (r) r.till = performance.now() + 60;
    };
    const told = (e: Event) => e.target instanceof HTMLElement && (moveLog("layoutMoves", "own-height", { note: nameOf(e.target) }), moves(e.target, (e as CustomEvent<number>).detail + 100, true));
    const animates = (e: AnimationEvent) => moveLog("css.animation", e.animationName, { note: e.target instanceof HTMLElement ? nameOf(e.target) : "" });
    el.addEventListener("transitionrun", runs);
    el.addEventListener("transitionend", ran);
    el.addEventListener("transitioncancel", ran);
    el.addEventListener(MOVES, told);
    el.addEventListener("animationstart", animates);
    el.addEventListener("wheel", wheel, { passive: true });
    for (const k of ["touchstart", "touchmove", "touchend", "touchcancel"] as const) el.addEventListener(k, touch, { passive: true });
    el.addEventListener("keydown", key);
    for (const k of ["pointerup", "keydown"] as const) addEventListener(k, act, true);
    return () => {
      ro.disconnect();
      clearTimeout(quietTimer.current);
      cancelAnimationFrame(frame.current);
      el.removeEventListener("transitionrun", runs);
      el.removeEventListener("transitionend", ran);
      el.removeEventListener("transitioncancel", ran);
      el.removeEventListener(MOVES, told);
      el.removeEventListener("animationstart", animates);
      el.removeEventListener("wheel", wheel);
      for (const k of ["touchstart", "touchmove", "touchend", "touchcancel"] as const) el.removeEventListener(k, touch);
      el.removeEventListener("keydown", key);
      for (const k of ["pointerup", "keydown"] as const) removeEventListener(k, act, true);
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
    if (el && height.current) follow(el, "pad", true);
  }, [padNow]);

  const scrolled = () => {
    const el = list.current!;
    const top = el.scrollTop;
    const d = top - lastTop.current;
    lastTop.current = top;
    // Where this put it, or is putting it, is no move of the user's (who goes up by the wheel, a
    // finger or a key, seen before the scroll comes).
    if (atEnd.current && (frame.current || Math.abs(top - ours.current) <= 1)) return;
    const gapNow = bottomOf(el) - top;
    if (!atEnd.current && hasBlend() && Math.abs(top - ours.current) <= 1 && gapNow > 0.5) return;
    // Nor where the browser pulled it to as the conversation got shorter: that is taken in first
    // (the scroll's event comes before the observer's word of it; read as the user's, the scroll
    // place was left a little short of the end, and the conversation went down and came back).
    const col = colOf(el);
    if (atEnd.current && col && (Math.abs(tall(col) - height.current) > 0.01 || padOf(el) !== pad.current || el.clientHeight !== paneH.current)) {
      follow(el, "?", performance.now() - actedAt.current < USER);
      lastTop.current = el.scrollTop;
      return;
    }
    const wasAtEnd = atEnd.current;
    moved.current = performance.now();
    // Any way up lets go of the newest at once, not only past END: a slow drag up was pulled back
    // down each time more came (the user, 2026-11-08). Only going down near it holds on again.
    const gap = bottomOf(el) - top;
    if (atEnd.current) {
      if (d < -0.5 && gap > 1) leave();
    } else if ((gap < END && d >= 0) || gap <= 1) atEnd.current = true; // at the very end too as what shrank below pulled it there
    // The user's own scrolling near the end (a smooth scroll down still on its way as it comes
    // within END): the scroll place is theirs, and what comes next is followed from there.
    // Sync height/moving on re-attach so a shrink that clamped scrollTop to gap <= 1 is already
    // absorbed and never triggers a room spacer + follow spring bounce (kumo, 2026-11-24).
    if (atEnd.current) {
      if (!wasAtEnd) {
        clearBlend();
        height.current = tall(col);
        pad.current = padOf(el);
        paneH.current = el.clientHeight;
        movedBy();
        mark(el);
      }
      setFresh(false);
      ours.current = top;
      closeSoon();
    }
    // Gone up from the newest: the room kept under it is let go, unseen from here.
    else if (room.current && gap > room.current + 1) setRoom(0);
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
        }} className="gapless absolute inset-0 overflow-y-auto [overflow-anchor:none] [scrollbar-gutter:stable_both-edges]" style={style}>
        {children}
        {/* The room kept for what shrank at the end (follow). */}
        <div ref={spacer} aria-hidden className="gapless" />
      </div>
      {fresh && !quiet && (
        <button
          type="button"
          onClick={goDown}
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
