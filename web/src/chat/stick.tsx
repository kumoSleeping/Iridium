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
const HOLD = 350; // ms with nothing changing before what shrank at the newest settles down

const padOf = (el: HTMLElement) => parseFloat(getComputedStyle(el).paddingBottom) || 0;

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
export function Stick({ tail, older, place, style, children, scroller, bottom, quiet }: { tail: unknown; older: () => Promise<boolean>; place: { top: number | null }; style?: CSSProperties; children: ReactNode; scroller?: RefObject<HTMLDivElement | null>; bottom: number; quiet?: boolean }) {
  const own = useRef<HTMLDivElement>(null);
  const list = scroller ?? own;
  const atEnd = useRef(place.top === null);
  const drawn = useRef<unknown>(undefined);
  const paging = useRef(false);
  const [fresh, setFresh] = useState(false);
  const [, redraw] = useState(0);
  // The glide: the conversation drawn as far below its place as it was pushed, easing to it, on the
  // compositor (not a frame drawn by script each time: the keyboard and the phone's sheet keep the
  // page busy as they move). A push while one runs adds to where that one is, so a draft growing
  // line by line flows on instead of stuttering.
  const anim = useRef<Animation | null>(null);
  /** The very end, by the layout alone: not the glide's overhang below it, which a transform adds
   *  to scrollHeight. */
  const bottomOf = (el: HTMLElement) => {
    const col = el.firstElementChild as HTMLElement | null;
    if (!col) return el.scrollHeight - el.clientHeight;
    return Math.max(0, col.offsetTop + col.offsetHeight + padOf(el) - el.clientHeight);
  };
  // The column's height and the pane's bottom padding (the stack under it) as last measured: 0
  // till the first page is in.
  const height = useRef(0);
  const pad = useRef(0);
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
    return el.scrollTop - was;
  };
  /** To the newest, gliding up by as far as that scrolled it (a message sent). */
  const toBottom = (el: HTMLElement) => glide(pin(el));
  /** What shrank at the newest (a draft giving way to its block, the turn's row going as the turn
   *  ends, the composer emptied) is held: the column kept as tall, nothing moving, till nothing has
   *  changed for HOLD, then it settles down once, gliding. Not a bounce down and back up each time
   *  something shrinks and grows again (the user, 2026-11-08). */
  const floorTimer = useRef(0);
  const settleSoon = () => {
    clearTimeout(floorTimer.current);
    floorTimer.current = window.setTimeout(settleFloor, HOLD);
  };
  const settleFloor = () => {
    const el = list.current;
    const col = el?.firstElementChild as HTMLElement | null;
    if (!el || !col || !col.style.minHeight) return;
    // Not under a finger: then once it is lifted.
    if (touching.current) return settleSoon();
    const top = el.scrollTop;
    col.style.minHeight = "";
    height.current = col.offsetHeight;
    pad.current = padOf(el);
    // Let go of meanwhile, it may still have pulled the scroll up, at its foot: that glides too.
    if (atEnd.current) pin(el);
    glide(el.scrollTop - top);
  };
  /** Kept at the newest: what came at the end, or the stack under it grown (the composer a line
   *  taller, the phone's sheet out), pushes the conversation up by as much, gliding; a short chat
   *  that does not scroll does not move at all. The pane itself shorter or taller (the phone's
   *  keyboard) glides nothing: the conversation goes with the pane's foot as it moves. */
  const follow = (el: HTMLElement, settling = false) => {
    const col = el.firstElementChild as HTMLElement | null;
    const was = height.current;
    const padWas = pad.current;
    height.current = col?.offsetHeight ?? 0;
    pad.current = padOf(el);
    // A fold opening or shutting (toggleFold): its own height glides, what is below it goes with
    // the layout; at the newest the end is only kept, frame by frame, nothing more gliding.
    if (performance.now() < folding.current) {
      if (atEnd.current) pin(el);
      return;
    }
    if (!atEnd.current) return;
    // Shrunk at the newest: held as tall (settleFloor).
    const lost = was + padWas - height.current - pad.current;
    if (col && was > 0 && lost > 0.5 && !paging.current) {
      col.style.minHeight = `${height.current + lost}px`;
      height.current += lost;
      settleSoon();
      return;
    }
    if (col?.style.minHeight) settleSoon();
    const by = pin(el);
    // Not the first page coming into an empty chat: that is drawn in place, at the newest.
    const want = was > 0 && !paging.current ? height.current - was + pad.current - padWas : 0;
    glide(want > 0 ? Math.min(want, Math.max(0, by)) : Math.max(want, Math.min(0, by)), settling);
  };
  const born = useRef(performance.now());
  /** settling: a push even as the chat settles (new blocks or drafts, not a picture loading). */
  const glide = (by: number, settling = false) => {
    // A chat just opened settles in place (its pictures, the composer's cards): nothing glides. What
    // came since it was last seen, brought as it opens, still does (the user, 2026-11-06).
    if (!settling && performance.now() - born.current < SETTLE) return;
    const el = list.current;
    const col = el?.firstElementChild as HTMLElement | null;
    if (!el || !col || !by || reduced() || document.hidden) return;
    let at = 0;
    if (anim.current?.playState === "running") {
      const tr = getComputedStyle(col).transform;
      at = tr && tr !== "none" ? new DOMMatrixReadOnly(tr).m42 : 0;
      anim.current.cancel();
    }
    const from = Math.max(-el.clientHeight, Math.min(el.clientHeight, at + by));
    anim.current = Math.abs(from) < 0.5 ? null : col.animate([{ transform: `translateY(${from}px)` }, { transform: "none" }], { duration: motion.dur(3), easing: motion.ease("out") });
  };

  /** Till when a fold's height glides (toggleFold): the growth meanwhile is its. */
  const folding = useRef(0);
  /** Fold d opened or shut by a press (the user, 2026-11-08: clean, no jump at either end): its
   *  height glides from what it is to what it will be, its rows fading, clipped to it; shutting,
   *  it shuts once the glide is done. Nothing is drawn where it is not: what is below it moves
   *  with the layout, so nothing has to settle back at the end. */
  const toggleFold = (d: HTMLDetailsElement) => {
    if (d.dataset.anim !== undefined) return;
    const opening = !d.open;
    const from = d.offsetHeight;
    if (opening) d.open = true;
    else d.dataset.shut = "";
    const sum = d.querySelector(":scope > summary") as HTMLElement | null;
    const to = opening ? d.offsetHeight : (sum?.offsetHeight ?? 0);
    const opts: KeyframeAnimationOptions = { duration: motion.dur(2), easing: motion.ease("out"), fill: "forwards" };
    folding.current = performance.now() + motion.dur(2) + 100;
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
        flushSync(() => redraw((n) => n + 1));
        el.scrollTop += el.scrollHeight - h;
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
    el.addEventListener("wheel", wheel, { passive: true });
    for (const k of ["touchstart", "touchmove", "touchend", "touchcancel"] as const) el.addEventListener(k, touch, { passive: true });
    el.addEventListener("keydown", key);
    return () => {
      ro.disconnect();
      clearTimeout(floorTimer.current);
      el.removeEventListener("wheel", wheel);
      for (const k of ["touchstart", "touchmove", "touchend", "touchcancel"] as const) el.removeEventListener(k, touch);
      el.removeEventListener("keydown", key);
      anim.current?.cancel();
      const end = bottomOf(el) - el.scrollTop < END;
      place.top = end ? null : el.scrollTop;
    };
  }, []);

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
