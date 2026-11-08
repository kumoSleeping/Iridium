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

const padOf = (el: HTMLElement) => parseFloat(getComputedStyle(el).paddingBottom) || 0;

/** Each chat's way to its newest, for what puts something at the end itself (a message sent). */
const ends = new WeakMap<HTMLElement, () => void>();
export const toEnd = (el: HTMLElement | null) => el && ends.get(el)?.();
/** Whether a chat rests at its very newest: there, and not scrolled by the user for a while (not a
 *  fling that just got there). */
const rests = new WeakMap<HTMLElement, () => boolean>();
export const restsAtEnd = (el: HTMLElement) => rests.get(el)?.() ?? false;

export function Stick({ tail, older, place, style, children, scroller, bottom }: { tail: unknown; older: () => Promise<boolean>; place: { top: number | null }; style?: CSSProperties; children: ReactNode; scroller?: RefObject<HTMLDivElement | null>; bottom: number }) {
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
  const pin = (el: HTMLElement) => {
    const was = el.scrollTop;
    el.scrollTop = bottomOf(el);
    ours.current = el.scrollTop;
    return el.scrollTop - was;
  };
  /** To the newest, gliding up by as far as that scrolled it (a message sent). */
  const toBottom = (el: HTMLElement) => glide(pin(el));
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
    if (!atEnd.current) return;
    const by = pin(el);
    // A fold opened or shut by a press: only what is above it goes, up or down, gliding; what is
    // below it (a message sent, the turn's row) stays where it was (the user, 2026-11-08).
    const toggled = toggledAt.current && performance.now() - toggledAt.current.t < 400 ? toggledAt.current.el : null;
    toggledAt.current = null;
    if (toggled && col && by && anim.current?.playState !== "running") return glideAbove(el, col, toggled, by);
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

  /** The fold pressed last, and when: the next growth is its. */
  const toggledAt = useRef<{ el: Element; t: number } | null>(null);
  /** The blocks from the top down to the one holding at, those in sight, drawn where they were
   *  (by px lower) and gliding to their place; the rest left still. */
  const glideAbove = (el: HTMLElement, col: HTMLElement, at: Element, by: number) => {
    if (reduced() || document.hidden) return;
    const top = el.scrollTop - Math.abs(by);
    const end = el.scrollTop + el.clientHeight + Math.abs(by);
    for (const c of col.children as HTMLCollectionOf<HTMLElement>) {
      if (c.offsetTop + c.offsetHeight > top && c.offsetTop < end) c.animate([{ transform: `translateY(${by}px)` }, { transform: "none" }], { duration: motion.dur(3), easing: motion.ease("out") });
      if (c.contains(at)) break;
    }
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
    return () => {
      ro.disconnect();
      anim.current?.cancel();
      const end = bottomOf(el) - el.scrollTop < END;
      place.top = end ? null : el.scrollTop;
    };
  }, []);

  const scrolled = () => {
    const el = list.current!;
    // Where this put it is no move of the user's: the pane may have shrunk again since (the
    // keyboard coming in steps), and it is still to keep at the newest.
    if (atEnd.current && Math.abs(el.scrollTop - ours.current) <= 1) return;
    moved.current = performance.now();
    atEnd.current = bottomOf(el) - el.scrollTop < END;
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
          if (fold) toggledAt.current = { el: fold, t: performance.now() };
        }} className="absolute inset-0 overflow-y-auto [overflow-anchor:none] [scrollbar-gutter:stable_both-edges]" style={style}>
        {children}
      </div>
      {fresh && (
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
