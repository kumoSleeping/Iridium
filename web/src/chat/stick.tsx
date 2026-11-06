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

/** Each chat's way to its newest, for what puts something at the end itself (a message sent). */
const ends = new WeakMap<HTMLElement, () => void>();
export const toEnd = (el: HTMLElement | null) => el && ends.get(el)?.();

export function Stick({ tail, older, place, style, children, scroller, bottom }: { tail: unknown; older: () => Promise<boolean>; place: { top: number | null }; style?: CSSProperties; children: ReactNode; scroller?: RefObject<HTMLDivElement | null>; bottom: number }) {
  const own = useRef<HTMLDivElement>(null);
  const list = scroller ?? own;
  const atEnd = useRef(place.top === null);
  const drawn = useRef<unknown>(undefined);
  const paging = useRef(false);
  const [fresh, setFresh] = useState(false);
  const [, redraw] = useState(0);
  // The glide: the conversation drawn this far below its place, easing to 0. A push adds to it, so
  // a draft growing line by line flows on instead of stuttering.
  const off = useRef(0);
  const frame = useRef(0);
  /** The very end, by the layout alone: not the glide's overhang below it, which a transform adds
   *  to scrollHeight. */
  const bottomOf = (el: HTMLElement) => {
    const col = el.firstElementChild as HTMLElement | null;
    if (!col) return el.scrollHeight - el.clientHeight;
    return Math.max(0, col.offsetTop + col.offsetHeight + (parseFloat(getComputedStyle(el).paddingBottom) || 0) - el.clientHeight);
  };
  // The column's height as last measured: 0 till the first page is in.
  const height = useRef(0);
  /** To the newest; push: the conversation glides up by as far as that scrolled it, so what was
   *  on screen does not jump (a short chat that does not scroll does not move at all). */
  const toBottom = (el: HTMLElement, push = false, settling = false) => {
    const was = el.scrollTop;
    el.scrollTop = bottomOf(el);
    if (push) glide(el.scrollTop - was, settling);
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
    off.current = Math.max(-el.clientHeight, Math.min(el.clientHeight, off.current + by));
    const tau = motion.dur(3) / 4;
    const draw = () => void (col.style.transform = Math.abs(off.current) < 0.5 ? "" : `translateY(${off.current}px)`);
    draw();
    if (frame.current) return;
    let last = performance.now();
    const step = (now: number) => {
      off.current *= Math.exp(-(now - last) / tau);
      last = now;
      if (Math.abs(off.current) < 0.5) off.current = 0;
      draw();
      frame.current = off.current ? requestAnimationFrame(step) : 0;
    };
    frame.current = requestAnimationFrame(step);
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
    if (first && place.top !== null) el.scrollTop = place.top;
    else if (first || atEnd.current) toBottom(el, !first && height.current > 0 && !paging.current, was !== tail);
    else if (was !== tail) setFresh(true);
    void back().catch(() => {});
  });

  // Content that grows (what the agent says, a picture loading, a fold opened at the bottom, the
  // composer growing) keeps it at the newest, the conversation pushed up by as much as it grew.
  // Read after layout, before paint: nothing is drawn in its new place first.
  useLayoutEffect(() => {
    const el = list.current!;
    const col = el.firstElementChild as HTMLElement | null;
    height.current = col?.offsetHeight ?? 0;
    const ro = new ResizeObserver(() => {
      const was = height.current;
      height.current = col?.offsetHeight ?? 0;
      // Not the first page coming into an empty chat: that is drawn in place, at the newest.
      if (atEnd.current) toBottom(el, !paging.current && was > 0);
    });
    ro.observe(el);
    if (col) ro.observe(col);
    ends.set(el, () => ((atEnd.current = true), setFresh(false), toBottom(el, true)));
    return () => {
      ro.disconnect();
      cancelAnimationFrame(frame.current);
      const end = bottomOf(el) - el.scrollTop < END;
      place.top = end ? null : el.scrollTop;
    };
  }, []);

  const scrolled = () => {
    const el = list.current!;
    atEnd.current = bottomOf(el) - el.scrollTop < END;
    if (atEnd.current) setFresh(false);
    void back().catch(() => {});
  };

  return (
    <>
      {/* A scrollbar that takes room (not an overlay one) has its width kept on the left too: the
          conversation stays centred in the pane, in line with the composer and the top bar under
          and over it, not off to the left by half a scrollbar (the user, 2026-11-06). */}
      <div ref={list} role="log" aria-label={t("chat.log")} onScroll={scrolled} className="absolute inset-0 overflow-y-auto [overflow-anchor:none] [scrollbar-gutter:stable_both-edges]" style={style}>
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
