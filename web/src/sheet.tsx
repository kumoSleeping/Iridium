// The sidebar on a phone (the user, 2026-11-06): a sheet from the window's foot, half its height,
// over the composer, which stays where it is, covered (it sinks only if taller than the sheet; the
// user, 2026-11-06). Nothing dims: the chat above is seen and scrolls as ever, and a tap on
// it puts the sheet away, as picking a session does. The chat bar's ⋯ menu brings it out; a swipe
// up from the composer, its box too, does too, following the finger; Esc puts it away. With no chat
// open it stays out, the whole window. While searching it stands taller, for the keyboard. The
// sidebar's foot, me and the settings key, is a sheet of its own, as tall as it: a swipe up from the
// composer's left quarter brings it, one from anywhere else the sessions' (the user, 2026-11-06). A
// chat at rest at its newest swiped on up brings them too, but only a long, plain swipe: not one
// that was just scrolling down to the newest (the user, 2026-11-06).
// Every sheet, the settings' and me's too, out: a swipe down from its grip or head puts it away,
// following the finger; held there a moment first, the grip lights and the finger makes it taller, or
// shorter down to its first height, as it stays on this device, what it holds scrolling, with no bar (the user,
// 2026-11-07). Let go near its first height, it goes back to that.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { restsAtEnd } from "./chat/stick";
import { motion } from "./motion";
import { buzz, cn, keep, kept } from "./ui";

/** Where a swipe up brings the sheet: the chat's bottom stack (chat.tsx), the composer in it. */
export const PULL = "data-sheet-pull";
/** Its grip's height, px. */
export const GRIP = 16;
const HEAD = 64; // px from the sheet's top where a swipe down or a hold may begin: the grip and the head
const KEPT = "sheetH."; // + its name: how tall the user left it, a share of the window
const HOLD = 220; // ms the grip or head is held still before the finger sizes the sheet (the user, 2026-11-07: 300 was long)
// The sheets sized as one, by name: the sessions' and the 终端 panel share a height (the user,
// 2026-11-07). Each told as one of them is let go.
const told = new Map<string, Set<(g: number) => void>>();
const SNAP = 28; // px from its first height (or the window's top) where, let go, it goes there
// From the conversation: the finger goes DEAD px up before the sheet moves, then the sheet follows
// at RESIST of its pace; let go, it comes out once OPEN of it is out (and the finger went MIN px past
// DEAD at least), or FLICK of it on a quick flick up. A long press first is choosing words: not a swipe.
const LOG = { DEAD: 32, RESIST: 0.7, OPEN: 0.4, FLICK: 0.22, MIN: 160, SPEED: 0.8, HOLD: 400 };
const SETTLED = 120; // ms its height is still before the chat is told it
/** Its first height, but the one as tall as what it holds. */
const FIRST = { half: "max(320px, 50dvh)", tall: "85dvh", full: "100dvh" } as const;
const firstPx = { half: () => Math.max(320, innerHeight / 2), tall: () => innerHeight * 0.85, full: () => innerHeight };

/** Whether a swipe up from x brings the foot's sheet: it began in the window's left quarter. */
export const atLeft = (x: number) => x < innerWidth / 4;
/** Whether a swipe up from x brings the notes: it began in the window's right quarter. */
export const atRight = (x: number) => x > (innerWidth * 3) / 4;

/** size: half the window, most of it, all of it (no chat open: not to be sized), or as tall as what
 *  it holds; made taller or shorter, as the user left it, kept by name. raised: at least most of
 *  the window (searching, for the keyboard). from: where across the window a swipe up may begin to
 *  bring it, told to setOpen as it comes out; onPull: told as a swipe up begins to bring it, where. head: px from its top that are its head, where a swipe
 *  down or a hold may begin, if not HEAD (its grip and the tabs' line, pager.tsx). */
export function Sheet({ open, setOpen, size: want, raised, name, head = 0, from: zone, onPull, label, onHeight, children }: { open: boolean; setOpen: (o: boolean, x?: number) => void; onPull?: (x: number) => void; size: "half" | "tall" | "full" | "fit"; raised?: boolean; name: string; head?: number; from: (x: number) => boolean; label: string; onHeight: (h: number) => void; children: ReactNode }) {
  const panel = useRef<HTMLDivElement>(null);
  const body = useRef<HTMLDivElement>(null);
  // Going down, it keeps its size till it is down: a chat picked from the whole window's sheet put
  // it away shrinking to half as it went, a second motion over the first (the user, 2026-11-08).
  const [size, setSize] = useState(want);
  if (open && size !== want) setSize(want);
  useEffect(() => {
    if (open || size === want) return;
    const t = setTimeout(() => setSize(want), motion.dur(3) + 40);
    return () => clearTimeout(t);
  }, [open, want, size]);
  // While a finger drags it: how far it is out, 0..1; null when it is not dragged.
  const [drag, setDrag] = useState<number | null>(null);
  // As tall as it was left, a share of the window (0: its first height), and its px while sized.
  const [grown, setGrown] = useState(() => Math.min(1, Number(kept(KEPT + name)) || 0));
  useEffect(() => {
    const all = told.get(name) ?? new Set();
    told.set(name, all);
    all.add(setGrown);
    return () => void all.delete(setGrown);
  }, [name]);
  const [live, setLive] = useState<number | null>(null);
  // Held: the grip lit, the finger sizing it.
  const [armed, setArmed] = useState(false);
  const now = useRef({ open, setOpen, zone, size, name, head, onPull });
  now.current = { open, setOpen, zone, size, name, head, onPull };

  // Its height, for the chat to keep its newest above it: once it is still, not each frame of it
  // growing or of the keyboard moving the window's foot, each a redraw of the whole page.
  useEffect(() => {
    const el = panel.current;
    if (!el) return;
    let timer = 0;
    const ro = new ResizeObserver(() => {
      clearTimeout(timer);
      timer = window.setTimeout(() => onHeight(el.offsetHeight), SETTLED);
    });
    ro.observe(el);
    onHeight(el.offsetHeight);
    return () => (ro.disconnect(), clearTimeout(timer));
  }, []);

  // Its height glides only as it is made another size (searching, let go taller), not as the
  // keyboard changes what dvh is: that would start the glide again each frame.
  const sizeIs = `${size} ${!!raised} ${live === null ? grown : "drag"}`;
  const [sized, setSized] = useState(sizeIs);
  const [sizing, setSizing] = useState(false);
  if (sizeIs !== sized) {
    setSized(sizeIs);
    setSizing(live === null && open);
  }
  useEffect(() => {
    if (!sizing) return;
    const t = setTimeout(() => setSizing(false), motion.dur(3) + 60);
    return () => clearTimeout(t);
  }, [sizing, sized]);

  useEffect(() => {
    if (!open) return;
    // Out, the keyboard goes: the composer it was for is put away.
    if (document.activeElement instanceof HTMLElement && !panel.current?.contains(document.activeElement)) document.activeElement.blur();
    const key = (e: KeyboardEvent) => e.key === "Escape" && !e.defaultPrevented && !document.querySelector("[role=dialog][data-open], [role=menu][data-open]") && setOpen(false);
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [open]);

  // A vertical swipe: up from the composer while shut (its box too while it has nothing to scroll), or
  // from the conversation resting at its newest; down from the grip or the head while out. Undecided
  // till it has moved 8px; then it is the sheet's only if it went more down or up than across, and a
  // way it may go. Held still there first, the finger sizes it instead, up or down.
  useEffect(() => {
    let from: { x: number; y: number; t: number; open: boolean; h: number; log: boolean; box: boolean } | null = null;
    let mine = false;
    let held = false; // the finger sizes it
    let hold = 0;
    let at = 0;
    let tall = 0; // its height as sized, px
    let first = 0; // its first height, px: let go near it, it goes back to it
    let travel = 0; // from the conversation: px the finger went up past LOG.DEAD
    let last = { y: 0, t: 0, v: 0 };
    const height = () => panel.current?.offsetHeight || innerHeight / 2;
    // As tall as what it holds: its grip, its foot and what is in its body.
    const fits = () => {
      const el = panel.current;
      const b = body.current;
      return el && b ? el.offsetHeight - b.clientHeight + ((b.firstElementChild as HTMLElement | null)?.offsetHeight ?? 0) : 0;
    };
    const noSelect = (on: boolean) => {
      const s = document.documentElement.style;
      s.userSelect = on ? "none" : "";
      s.setProperty("-webkit-user-select", on ? "none" : "");
    };
    const arm = () => {
      if (!from || mine) return;
      held = true;
      const { size } = now.current;
      first = size === "fit" ? fits() : firstPx[size]();
      setArmed(true);
      noSelect(true);
      getSelection()?.removeAllRanges();
      // Ours at once, on Android too: its browser's own comes only with its contextmenu, later, and
      // only if the finger is still by then.
      buzz();
    };
    const noClick = (e: MouseEvent) => (e.stopPropagation(), e.preventDefault());
    const swallowClick = (ms: number) => {
      addEventListener("click", noClick, { capture: true, once: true });
      setTimeout(() => removeEventListener("click", noClick, true), ms);
    };
    // A finger, or a mouse (a computer's window as narrow as a phone's): the mouse only drags or
    // holds the grip or the head of a sheet that is out, not a swipe up to bring one.
    const begin = (x: number, y: number, t: number, el: Element | null, mouse: boolean) => {
      if (!el?.closest) return;
      const { open } = now.current;
      if (open) {
        const box = panel.current?.getBoundingClientRect();
        if (!box || !panel.current!.contains(el) || y - box.top > (now.current.head || HEAD) || el.closest("[role=slider], .touch-none")) return;
      } else {
        // The composer's box too, while it has nothing to scroll: all of it, as it first is (the
        // user, 2026-11-07); grown past its height, it scrolls its words.
        const box = el.closest("textarea");
        if (mouse || (box && box.scrollHeight > box.clientHeight + 1) || el.closest("input, [role=listbox], [role=dialog], [role=menu]") || !now.current.zone(x)) return;
        // In the conversation: only at rest at its newest, no words chosen.
        const log = el.closest<HTMLElement>("[role=log]");
        if (log ? !restsAtEnd(log) || !getSelection()?.isCollapsed : !el.closest(`[${PULL}]`)) return;
      }
      from = { x, y, t, open, h: height(), log: !open && !!el.closest("[role=log]"), box: !open && !!el.closest("textarea") };
      mine = false;
      held = false;
      last = { y, t, v: 0 };
      clearTimeout(hold);
      // Not on a box of words: held, that places the caret.
      if (open && now.current.size !== "full" && !el.closest("input, textarea")) hold = window.setTimeout(arm, HOLD);
    };
    const start = (e: TouchEvent) => {
      const p = e.touches[0];
      if (e.touches.length !== 1 || !p) return;
      begin(p.clientX, p.clientY, e.timeStamp, e.target as Element | null, false);
    };
    // Whether it went the sheet's way: then the event is its own (no scroll, no words chosen).
    const step = (x: number, y: number, t: number) => {
      if (!from) return false;
      const dy = y - from.y;
      if (held) {
        // Held, the finger sizes it, from its first height up to the window's top, never shorter
        // than it came (the user, 2026-11-07); nothing under it scrolls.
        if (!mine && Math.abs(dy) < 4) return true;
        mine = true;
        tall = Math.max(Math.min(first, innerHeight), Math.min(innerHeight, from.h - dy));
        setLive(tall);
        return true;
      }
      if (!mine) {
        const dx = Math.abs(x - from.x);
        if ((from.log || from.box) && t - from.t > LOG.HOLD) {
          from = null;
          return false;
        }
        if (Math.max(Math.abs(dy), dx) < 8) return false;
        clearTimeout(hold);
        if (Math.abs(dy) < dx * (from.log ? 1.5 : 1.2) || (from.open ? dy < 0 : dy > 0)) {
          from = null;
          return false;
        }
        mine = true;
        // Coming out: what it will show, told before it is seen.
        if (!from.open) now.current.onPull?.(from.x);
      }
      if (from.log) {
        travel = Math.max(0, -dy - LOG.DEAD);
        at = Math.min(1, (travel * LOG.RESIST) / from.h);
      } else at = Math.min(1, Math.max(0, (from.open ? from.h - dy : -dy) / from.h));
      last = { y, t, v: (y - last.y) / Math.max(1, t - last.t) };
      setDrag(at);
      return true;
    };
    const move = (e: TouchEvent) => {
      const p = e.touches[0];
      if (p && step(p.clientX, p.clientY, e.timeStamp) && e.cancelable) e.preventDefault();
    };
    const end = () => {
      clearTimeout(hold);
      if (from && held) {
        // Let go near its first height it goes back to it, near the window's top up to it; else
        // it stays as tall as it was made, on this device.
        if (mine) {
          const g = Math.abs(tall - first) <= SNAP ? 0 : tall >= innerHeight - SNAP ? 1 : tall / innerHeight;
          told.get(now.current.name)?.forEach((f) => f(g));
          keep(KEPT + now.current.name, g ? String(g) : null);
        }
        setLive(null);
        setArmed(false);
        noSelect(false);
        // No tap where it was held.
        swallowClick(500);
      } else if (from && mine) {
        // A flick decides by its way; a slow drag by where it was let go: put away once a quarter of
        // it went down.
        const out = from.log
          ? travel >= LOG.MIN && (at >= LOG.OPEN || (at >= LOG.FLICK && last.v < -LOG.SPEED))
          : Math.abs(last.v) > 0.4
            ? last.v < 0
            : at > (from.open ? 0.75 : 0.5);
        now.current.setOpen(out, from.x);
        setDrag(null);
      }
      from = null;
      mine = false;
      held = false;
    };
    // Held on its head: no menu of the page's own; it is the hold if the timer has not been yet.
    const menu = (e: Event) => {
      if (!from?.open) return;
      e.preventDefault();
      if (!held && now.current.size !== "full") (clearTimeout(hold), arm());
    };
    // The mouse: no words chosen while it drags, and no click where it is let go.
    let pressed = false;
    const mdown = (e: PointerEvent) => {
      if (e.pointerType !== "mouse" || e.button !== 0) return;
      begin(e.clientX, e.clientY, e.timeStamp, e.target as Element | null, true);
      pressed = !!from;
    };
    const mmove = (e: PointerEvent) => {
      if (!pressed) return;
      if (step(e.clientX, e.clientY, e.timeStamp)) {
        e.preventDefault();
        getSelection()?.removeAllRanges();
        noSelect(true);
      } else if (!from) pressed = false;
    };
    const mup = () => {
      if (!pressed) return;
      pressed = false;
      if (mine && !held) swallowClick(0);
      noSelect(false);
      end();
    };
    addEventListener("touchstart", start, { passive: true });
    addEventListener("touchmove", move, { passive: false });
    addEventListener("touchend", end);
    addEventListener("touchcancel", end);
    addEventListener("contextmenu", menu);
    addEventListener("pointerdown", mdown);
    addEventListener("pointermove", mmove);
    addEventListener("pointerup", mup);
    addEventListener("pointercancel", mup);
    return () => {
      clearTimeout(hold);
      removeEventListener("touchstart", start);
      removeEventListener("touchmove", move);
      removeEventListener("touchend", end);
      removeEventListener("touchcancel", end);
      removeEventListener("contextmenu", menu);
      removeEventListener("pointerdown", mdown);
      removeEventListener("pointermove", mmove);
      removeEventListener("pointerup", mup);
      removeEventListener("pointercancel", mup);
      removeEventListener("click", noClick, true);
      noSelect(false);
    };
  }, []);

  const out = drag ?? (open ? 1 : 0);
  // As tall as it was left, else its first height; searching, at least most of the window.
  const left = size === "fit" ? (grown ? `${grown * 100}dvh` : undefined) : grown ? `max(${FIRST[size]}, ${grown * 100}dvh)` : FIRST[size];
  const height = size === "full" ? FIRST.full : live !== null ? live : raised ? `max(${FIRST.tall}, ${left ?? "0px"})` : left;
  const fixed = size !== "fit" || grown > 0 || live !== null;
  return (
    <div
      ref={panel}
      role="region"
      aria-label={label}
      inert={!open && drag === null}
      className={cn(
        "g-sheet vt-drawer fixed inset-x-0 bottom-0 z-40 flex max-h-dvh flex-col overflow-hidden rounded-t-2xl bg-paper pb-[env(safe-area-inset-bottom)] shadow-[var(--shadow-float)] [-webkit-touch-callout:none]",
        // Up to the window's top: under the status bar, its corners square.
        (size === "full" || (live === null && grown >= 0.99)) && "rounded-t-none pt-[env(safe-area-inset-top)]",
        sizing && "g-sizing",
        drag !== null && "[transition:none]",
        out === 0 && "shadow-none",
      )}
      // Shut, past its own height and its shadow too.
      style={{ transform: `translateY(calc(${(1 - out) * 100}% + ${(1 - out) * 20}px))`, height }}
    >
      <div aria-hidden className="grid h-4 shrink-0 place-items-end justify-center">
        <span className={cn("h-1 w-9 rounded-full bg-fill-2 transition-[width,background-color] duration-(--dur-2) ease-(--ease-out)", armed && "w-14 bg-ink-3")} />
      </div>
      {/* What it holds scrolls, with no bar; as tall as it, the one sized to fit. */}
      <div ref={body} className={cn("relative min-h-0", fixed && "flex-1", size === "fit" && "no-bar overflow-y-auto overscroll-contain")}>
        {children}
      </div>
    </div>
  );
}
