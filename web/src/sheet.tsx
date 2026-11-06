// The sidebar on a phone (the user, 2026-11-06): a sheet from the window's foot, half its height,
// taking the composer's place: one of the two is at the bottom at a time, the composer sinking as
// the sheet rises and back. Nothing dims: the chat above is seen and scrolls as ever, and a tap on
// it puts the sheet away, as picking a session does. The chat bar's ⋯ menu brings it out; a swipe
// up from the composer does too, following the finger, and one down on its grip or head puts it
// away; Esc does too. With no chat open it stays out. While searching it stands taller, for the
// keyboard. Its grip dragged up makes it taller, to three quarters of the window at most, and it
// stays so on this device (the user, 2026-11-06). The sidebar's foot, me and the settings key, is a sheet of its own, only as tall as it:
// a swipe up from the composer's left quarter brings it, one from anywhere else the sessions'
// (the user, 2026-11-06).
import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn, keep, kept } from "./ui";

/** Where a swipe up brings the sheet: the chat's bottom stack (chat.tsx), the composer in it. */
export const PULL = "data-sheet-pull";
const HEAD = 64; // px from the sheet's top where a swipe down may begin: the grip and the head
const GROWN = "sheetGrown"; // how tall the user left the half sheet, a share of the window
const MAX = 0.75;
/** The half sheet's height as it comes, and the most it may be made. */
const baseH = () => Math.max(320, innerHeight / 2);
const maxH = () => Math.max(baseH(), innerHeight * MAX);

/** Whether a swipe up from x brings the foot's sheet: it began in the window's left quarter. */
export const atLeft = (x: number) => x < innerWidth / 4;

/** size: half the window, most of it (searching), or as tall as what it holds. from: where across
 *  the window a swipe up may begin to bring it. */
export function Sheet({ open, setOpen, size, from: zone, label, onHeight, children }: { open: boolean; setOpen: (o: boolean) => void; size: "half" | "tall" | "fit"; from: (x: number) => boolean; label: string; onHeight: (h: number) => void; children: ReactNode }) {
  const panel = useRef<HTMLDivElement>(null);
  // While a finger drags it: how far it is out, 0..1; null when it is not dragged.
  const [drag, setDrag] = useState<number | null>(null);
  // The half sheet made taller: a share of the window as it was left, and its px while dragged.
  const [grown, setGrown] = useState(() => Math.min(MAX, Number(kept(GROWN)) || 0));
  const [live, setLive] = useState<number | null>(null);
  const now = useRef({ open, setOpen, zone, size });
  now.current = { open, setOpen, zone, size };

  // Its height, for the chat to keep its newest above it.
  useEffect(() => {
    const el = panel.current;
    if (!el) return;
    const ro = new ResizeObserver(() => onHeight(el.offsetHeight));
    ro.observe(el);
    onHeight(el.offsetHeight);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    if (!open) return;
    // Out, the keyboard goes: the composer it was for is put away.
    if (document.activeElement instanceof HTMLElement && !panel.current?.contains(document.activeElement)) document.activeElement.blur();
    const key = (e: KeyboardEvent) => e.key === "Escape" && !e.defaultPrevented && !document.querySelector("[role=dialog][data-open], [role=menu][data-open]") && setOpen(false);
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [open]);

  // A vertical swipe: up from the composer while shut (not in its box, which scrolls its words),
  // down from the grip or the head while out, or up there to make the half sheet taller. Undecided
  // till it has moved 8px; then it is the sheet's only if it went more down or up than across, and
  // a way it may go.
  useEffect(() => {
    let from: { x: number; y: number; open: boolean; h: number } | null = null;
    let mine = false;
    let at = 0;
    let tall = 0; // the half sheet's height as dragged, px
    let last = { y: 0, t: 0, v: 0 };
    const height = () => panel.current?.offsetHeight || innerHeight / 2;
    const start = (e: TouchEvent) => {
      const p = e.touches[0];
      const el = e.target as Element | null;
      if (e.touches.length !== 1 || !p || !el?.closest) return;
      const { open } = now.current;
      if (open) {
        const box = panel.current?.getBoundingClientRect();
        if (!box || !panel.current!.contains(el) || p.clientY - box.top > HEAD) return;
      } else if (!el.closest(`[${PULL}]`) || el.closest("textarea, [role=listbox], [role=dialog], [role=menu]") || !now.current.zone(p.clientX)) return;
      from = { x: p.clientX, y: p.clientY, open, h: height() };
      mine = false;
      last = { y: p.clientY, t: e.timeStamp, v: 0 };
    };
    const move = (e: TouchEvent) => {
      const p = e.touches[0];
      if (!from || !p) return;
      const dy = p.clientY - from.y;
      if (!mine) {
        const dx = Math.abs(p.clientX - from.x);
        if (Math.abs(dy) < 8) return;
        if (Math.abs(dy) < dx * 1.2 || (from.open ? dy < 0 && now.current.size !== "half" : dy > 0)) return void (from = null);
        mine = true;
      }
      e.preventDefault();
      if (from.open && now.current.size === "half") {
        // Above its first height it grows and shrinks; below that it goes as when put away.
        const want = from.h - dy;
        const base = Math.min(from.h, baseH());
        tall = Math.max(base, Math.min(maxH(), want));
        setLive(tall);
        at = Math.min(1, Math.max(0, want / base));
      } else at = Math.min(1, Math.max(0, (from.open ? from.h - dy : -dy) / from.h));
      last = { y: p.clientY, t: e.timeStamp, v: (p.clientY - last.y) / Math.max(1, e.timeStamp - last.t) };
      setDrag(at);
    };
    const end = () => {
      if (from && mine) {
        if (from.open && now.current.size === "half") {
          // Let go still out: it stays as tall as it was made; below that, back to its first height.
          const g = at === 1 && tall > baseH() + 1 ? Math.min(MAX, tall / innerHeight) : 0;
          setGrown(g);
          keep(GROWN, g ? String(g) : "");
          setLive(null);
        }
        // A flick decides by its way; a slow drag by where it was let go; still above its first
        // height, it stays out.
        const out = at === 1 && from.open ? true : Math.abs(last.v) > 0.4 ? last.v < 0 : at > 0.5;
        now.current.setOpen(out);
        setDrag(null);
      }
      from = null;
      mine = false;
    };
    addEventListener("touchstart", start, { passive: true });
    addEventListener("touchmove", move, { passive: false });
    addEventListener("touchend", end);
    addEventListener("touchcancel", end);
    return () => {
      removeEventListener("touchstart", start);
      removeEventListener("touchmove", move);
      removeEventListener("touchend", end);
      removeEventListener("touchcancel", end);
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
        size === "tall" ? "h-[85dvh]" : size === "half" ? "h-[50dvh] min-h-[320px]" : "",
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
