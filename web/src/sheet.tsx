// The sidebar on a phone (the user, 2026-11-06): a sheet from the window's foot, half its height,
// over the composer, which stays where it is, covered (it sinks only if taller than the sheet; the
// user, 2026-11-06). Nothing dims: the chat above is seen and scrolls as ever, and a tap on
// it puts the sheet away, as picking a session does. The chat bar's ⋯ menu brings it out; a swipe
// up from the composer does too, following the finger, and one down on its grip or head puts it
// away; Esc does too. With no chat open it stays out. While searching it stands taller, for the
// keyboard. Its grip dragged makes it taller, up to the window's top, or shorter, down to its head
// alone, and it stays so on this device; with no chat open it is the whole window, whatever was kept (the user, 2026-11-06). The sidebar's foot, me and the settings key, is a sheet of its own, only as tall as it:
// a swipe up from the composer's left quarter brings it, one from anywhere else the sessions'
// (the user, 2026-11-06). A chat at rest at its newest swiped on up brings them too, but only a
// long, plain swipe: not one that was just scrolling down to the newest (the user, 2026-11-06).
import { useEffect, useRef, useState, type ReactNode } from "react";
import { restsAtEnd } from "./chat/stick";
import { motion } from "./motion";
import { cn, keep, kept } from "./ui";

/** Where a swipe up brings the sheet: the chat's bottom stack (chat.tsx), the composer in it. */
export const PULL = "data-sheet-pull";
const HEAD = 64; // px from the sheet's top where a swipe down may begin: the grip and the head
const GROWN = "sheetGrown"; // how tall the user left the half sheet, a share of the window
const MAX = 1;
// From the conversation: the finger goes DEAD px up before the sheet moves, then the sheet follows
// at RESIST of its pace; let go, it comes out once OPEN of it is out (and the finger went MIN px past
// DEAD at least), or FLICK of it on a quick flick up. A long press first is choosing words: not a swipe.
const LOG = { DEAD: 32, RESIST: 0.7, OPEN: 0.4, FLICK: 0.22, MIN: 160, SPEED: 0.8, HOLD: 400 };
const SETTLED = 120; // ms its height is still before the chat is told it
/** The half sheet's height as it comes, and the most it may be made. */
const baseH = () => Math.max(320, innerHeight / 2);
const maxH = () => Math.max(baseH(), innerHeight * MAX);

/** Whether a swipe up from x brings the foot's sheet: it began in the window's left quarter. */
export const atLeft = (x: number) => x < innerWidth / 4;

/** size: half the window (or as tall as it was left), most of it (searching), all of it (no chat
 *  open), or as tall as what it holds. from: where across
 *  the window a swipe up may begin to bring it. */
export function Sheet({ open, setOpen, size, from: zone, label, onHeight, children }: { open: boolean; setOpen: (o: boolean) => void; size: "half" | "tall" | "full" | "fit"; from: (x: number) => boolean; label: string; onHeight: (h: number) => void; children: ReactNode }) {
  const panel = useRef<HTMLDivElement>(null);
  // While a finger drags it: how far it is out, 0..1; null when it is not dragged.
  const [drag, setDrag] = useState<number | null>(null);
  // The half sheet made taller: a share of the window as it was left, and its px while dragged.
  const [grown, setGrown] = useState(() => Math.min(MAX, Number(kept(GROWN)) || 0));
  const [live, setLive] = useState<number | null>(null);
  const now = useRef({ open, setOpen, zone, size });
  now.current = { open, setOpen, zone, size };

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
  const sizeIs = `${size} ${live === null ? grown : "drag"}`;
  const [sized, setSized] = useState(sizeIs);
  const [sizing, setSizing] = useState(false);
  if (sizeIs !== sized) {
    setSized(sizeIs);
    setSizing(live === null);
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

  // A vertical swipe: up from the composer while shut (not in its box, which scrolls its words), or
  // from the conversation resting at its newest; down from the grip or the head while out, or up
  // there to make the half sheet taller. Undecided till it has moved 8px; then it is the sheet's
  // only if it went more down or up than across, and a way it may go.
  useEffect(() => {
    let from: { x: number; y: number; t: number; open: boolean; h: number; log: boolean } | null = null;
    let mine = false;
    let at = 0;
    let tall = 0; // the half sheet's height as dragged, px
    let travel = 0; // from the conversation: px the finger went up past LOG.DEAD
    let last = { y: 0, t: 0, v: 0 };
    const height = () => panel.current?.offsetHeight || innerHeight / 2;
    // The least it may be made: its grip and the sidebar's head, over the window's safe foot.
    const minH = () => HEAD + (panel.current ? parseFloat(getComputedStyle(panel.current).paddingBottom) || 0 : 0);
    // A finger, or a mouse (a computer's window as narrow as a phone's): the mouse only drags the
    // grip or the head of a sheet that is out, not a swipe up to bring one.
    const begin = (x: number, y: number, t: number, el: Element | null, mouse: boolean) => {
      if (!el?.closest) return;
      const { open } = now.current;
      if (open) {
        const box = panel.current?.getBoundingClientRect();
        if (!box || !panel.current!.contains(el) || y - box.top > HEAD) return;
      } else {
        if (mouse || el.closest("textarea, input, [role=listbox], [role=dialog], [role=menu]") || !now.current.zone(x)) return;
        // In the conversation: only at rest at its newest, no words chosen.
        const log = el.closest<HTMLElement>("[role=log]");
        if (log ? !restsAtEnd(log) || !getSelection()?.isCollapsed : !el.closest(`[${PULL}]`)) return;
      }
      from = { x, y, t, open, h: height(), log: !open && !!el.closest("[role=log]") };
      mine = false;
      last = { y, t, v: 0 };
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
      if (!mine) {
        const dx = Math.abs(x - from.x);
        if (from.log && t - from.t > LOG.HOLD) {
          from = null;
          return false;
        }
        if (Math.abs(dy) < 8) return false;
        if (Math.abs(dy) < dx * (from.log ? 1.5 : 1.2) || (from.open ? dy < 0 && now.current.size !== "half" : dy > 0)) {
          from = null;
          return false;
        }
        mine = true;
      }
      if (from.log) {
        travel = Math.max(0, -dy - LOG.DEAD);
        at = Math.min(1, (travel * LOG.RESIST) / from.h);
      } else if (from.open && now.current.size === "half") {
        // Down to its head alone it grows and shrinks (the user, 2026-11-06); below that it goes as
        // when put away.
        const want = from.h - dy;
        const base = Math.min(from.h, minH());
        tall = Math.max(base, Math.min(maxH(), want));
        setLive(tall);
        at = Math.min(1, Math.max(0, want / base));
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
      if (from && mine) {
        if (from.open && now.current.size === "half") {
          // Let go still out: it stays as tall as it was made (back at its first height, nothing kept);
          // put away, as it was.
          if (at === 1) {
            const g = Math.abs(tall - baseH()) > 1 ? Math.min(MAX, tall / innerHeight) : 0;
            setGrown(g);
            keep(GROWN, g ? String(g) : "");
          }
          setLive(null);
        }
        // A flick decides by its way; a slow drag by where it was let go; still above its first
        // height, it stays out.
        const out = from.log
          ? travel >= LOG.MIN && (at >= LOG.OPEN || (at >= LOG.FLICK && last.v < -LOG.SPEED))
          : from.open && now.current.size === "half"
            ? at === 1 || last.v < -0.4
            : at === 1 && from.open
            ? true
            : Math.abs(last.v) > 0.4
              ? last.v < 0
              : at > 0.5;
        now.current.setOpen(out);
        setDrag(null);
      }
      from = null;
      mine = false;
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
        document.documentElement.style.userSelect = "none";
      } else if (!from) pressed = false;
    };
    const noClick = (e: MouseEvent) => (e.stopPropagation(), e.preventDefault());
    const mup = () => {
      if (!pressed) return;
      pressed = false;
      if (mine) {
        addEventListener("click", noClick, { capture: true, once: true });
        setTimeout(() => removeEventListener("click", noClick, true), 0);
      }
      document.documentElement.style.userSelect = "";
      end();
    };
    addEventListener("touchstart", start, { passive: true });
    addEventListener("touchmove", move, { passive: false });
    addEventListener("touchend", end);
    addEventListener("touchcancel", end);
    addEventListener("pointerdown", mdown);
    addEventListener("pointermove", mmove);
    addEventListener("pointerup", mup);
    addEventListener("pointercancel", mup);
    return () => {
      removeEventListener("touchstart", start);
      removeEventListener("touchmove", move);
      removeEventListener("touchend", end);
      removeEventListener("touchcancel", end);
      removeEventListener("pointerdown", mdown);
      removeEventListener("pointermove", mmove);
      removeEventListener("pointerup", mup);
      removeEventListener("pointercancel", mup);
      removeEventListener("click", noClick, true);
      document.documentElement.style.userSelect = "";
    };
  }, []);

  const out = drag ?? (open ? 1 : 0);
  return (
    <div
      ref={panel}
      role="region"
      aria-label={label}
      inert={!open && drag === null}
      className={cn(
        "g-sheet vt-drawer fixed inset-x-0 bottom-0 z-40 flex flex-col overflow-hidden rounded-t-2xl bg-paper pb-[env(safe-area-inset-bottom)] shadow-[var(--shadow-float)]",
        size === "full" ? "h-dvh" : size === "tall" ? "h-[85dvh]" : size === "half" ? cn("h-[50dvh]", live === null && !grown && "min-h-[320px]") : "",
        // Up to the window's top: under the status bar, its corners square.
        (size === "full" || (size === "half" && live === null && grown >= 0.99)) && "rounded-t-none pt-[env(safe-area-inset-top)]",
        sizing && "g-sizing",
        drag !== null && "[transition:none]",
        out === 0 && "shadow-none",
      )}
      // Shut, past its own height and its shadow too.
      style={{ transform: `translateY(calc(${(1 - out) * 100}% + ${(1 - out) * 20}px))`, height: size !== "half" ? undefined : live !== null ? live : grown ? `${grown * 100}dvh` : undefined }}
    >
      <div aria-hidden className="grid h-4 shrink-0 place-items-end justify-center">
        <span className="h-1 w-9 rounded-full bg-fill-2" />
      </div>
      <div className={cn("relative min-h-0", size !== "fit" && "flex-1")}>{children}</div>
    </div>
  );
}
