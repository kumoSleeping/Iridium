// On a phone the bottom sheet holds its pages side by side, 设置, 用量 and 会话 (the user, 2026-11-08):
// tabs at its head's left say which is shown, their pill following the finger, the shown page's
// keys at its right, one line under the grip, apart from it (the user, 2026-11-08); a swipe across
// the page, following the finger too, goes to the next, as a press on its tab does. Not a swipe that began
// on what scrolls across, a slider, a box of words, or a menu.
import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { cn } from "./ui";

// The finger goes DECIDE px before it is a swipe across, more across than up or down by RATIO; let
// go past GO of the width, or flicked faster than SPEED px/ms, it turns the page. Past the first or
// the last page it moves at EDGE of the finger's pace.
const SWIPE = { DECIDE: 10, RATIO: 1.2, GO: 0.25, SPEED: 0.4, EDGE: 0.25 };

/** The head's height, px, its gap over it too: the sheet's head reaches past it (sheet.tsx head). */
export const TABS_H = 52;

/** lead: before the tabs (the mark, the sheet the whole window); ends: each page's keys at the right. */
export function Pager({
  page,
  onPage,
  tabs,
  lead,
  ends = [],
  still,
  children,
}: {
  page: number;
  onPage: (i: number) => void;
  tabs: { label: ReactNode; aria: string }[];
  lead?: ReactNode;
  ends?: ReactNode[];
  still?: boolean;
  children: ReactNode[];
}) {
  const track = useRef<HTMLDivElement>(null);
  // While a finger drags it: px across from where the page rests.
  const [dx, setDx] = useState<number | null>(null);
  const now = useRef({ page, onPage, n: children.length });
  now.current = { page, onPage, n: children.length };

  useEffect(() => {
    const el = track.current;
    if (!el) return;
    let from: { x: number; y: number } | null = null;
    let mine = false;
    let last = { x: 0, t: 0, v: 0 };
    const across = (t: Element | null) => {
      for (let e = t; e && e !== el; e = e.parentElement) if (e.scrollWidth > e.clientWidth + 1 && /auto|scroll/.test(getComputedStyle(e).overflowX)) return true;
      return false;
    };
    const start = (e: TouchEvent) => {
      const p = e.touches[0];
      const target = e.target as Element | null;
      from = null;
      if (e.touches.length !== 1 || !p || !target?.closest || target.closest("input, textarea, [role=slider], .touch-none, [role=dialog], [role=menu]") || across(target)) return;
      from = { x: p.clientX, y: p.clientY };
      mine = false;
      last = { x: p.clientX, t: e.timeStamp, v: 0 };
    };
    const move = (e: TouchEvent) => {
      const p = e.touches[0];
      if (!from || !p) return;
      const x = p.clientX - from.x;
      const y = p.clientY - from.y;
      if (!mine) {
        if (Math.max(Math.abs(x), Math.abs(y)) < SWIPE.DECIDE) return;
        // Up or down, or the page already scrolling: not ours.
        if (Math.abs(x) < Math.abs(y) * SWIPE.RATIO || !e.cancelable || e.defaultPrevented) {
          from = null;
          return;
        }
        mine = true;
      }
      e.preventDefault();
      const { page, n } = now.current;
      const edge = (page === 0 && x > 0) || (page === n - 1 && x < 0);
      setDx(edge ? x * SWIPE.EDGE : x);
      last = {
        x: p.clientX,
        t: e.timeStamp,
        v: (p.clientX - last.x) / Math.max(1, e.timeStamp - last.t),
      };
    };
    const end = () => {
      if (from && mine) {
        const { page, n, onPage } = now.current;
        const x = last.x - from.x;
        const way = Math.abs(last.v) > SWIPE.SPEED ? -Math.sign(last.v) : Math.abs(x) > el.clientWidth * SWIPE.GO ? -Math.sign(x) : 0;
        const to = Math.max(0, Math.min(n - 1, page + way));
        if (to !== page) onPage(to);
        setDx(null);
      }
      from = null;
      mine = false;
    };
    el.addEventListener("touchstart", start, { passive: true });
    el.addEventListener("touchmove", move, { passive: false });
    el.addEventListener("touchend", end);
    el.addEventListener("touchcancel", end);
    return () => {
      el.removeEventListener("touchstart", start);
      el.removeEventListener("touchmove", move);
      el.removeEventListener("touchend", end);
      el.removeEventListener("touchcancel", end);
    };
  }, []);

  // Where the tabs' pill stands: the page shown, or between the two as the finger drags.
  const at = Math.max(0, Math.min(children.length - 1, page - (dx ?? 0) / (track.current?.clientWidth || innerWidth)));
  // Out of sight (still): it goes to its page at once, not seen sliding as the sheet comes.
  const glide = dx === null && !still && "transition-transform duration-(--dur-3) ease-(--ease-out)";
  return (
    <div className="absolute inset-0 flex flex-col">
      <div className="mt-2 flex h-11 shrink-0 items-center gap-2 px-3 shadow-[0_0.5px_0_var(--edge)]">
        {lead}
        <div role="tablist" className="relative grid h-8 shrink-0 rounded-full bg-fill p-0.5" style={{ width: `${tabs.length * 4.5}rem`, gridTemplateColumns: `repeat(${tabs.length}, 1fr)` }}>
          <span
            aria-hidden
            className={cn("absolute inset-y-0.5 left-0.5 rounded-full bg-paper shadow-[0_0_0_0.5px_var(--edge),0_1px_2px_var(--edge)]", glide)}
            style={{
              width: `calc((100% - 4px) / ${tabs.length})`,
              transform: `translateX(${at * 100}%)`,
            }}
          />
          {tabs.map((tab, i) => (
            <button
              key={i}
              type="button"
              role="tab"
              aria-selected={i === page}
              aria-label={tab.aria}
              onClick={() => i !== page && onPage(i)}
              className={cn("relative flex items-center justify-center gap-1.5 text-sm transition-colors duration-(--dur-2)", Math.round(at) === i ? "font-medium text-ink" : "text-ink-3")}
            >
              {tab.label}
            </button>
          ))}
        </div>
        {/* Each page's keys where they stand, the one shown faded in as the finger drags. */}
        <div className="relative ml-auto flex h-full items-center">
          {ends.map((e, i) => (
            <div
              key={i}
              inert={i !== page}
              className={cn("absolute inset-y-0 right-0 flex items-center", dx === null && "transition-opacity duration-(--dur-2)")}
              style={{ opacity: Math.max(0, 1 - Math.abs(at - i)) }}
            >
              {e}
            </div>
          ))}
        </div>
      </div>
      <div ref={track} className="relative min-h-0 flex-1 overflow-hidden">
        {/* Its own layer: a page turned moves it, nothing drawn again. */}
        <div
          className={cn("flex h-full will-change-transform", glide)}
          style={
            {
              transform: `translateX(calc(${-page * 100}% + ${dx ?? 0}px))`,
            } as CSSProperties
          }
        >
          {children.map((c, i) => (
            <div key={i} role="tabpanel" inert={i !== page && dx === null} className="relative h-full w-full shrink-0">
              {c}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
