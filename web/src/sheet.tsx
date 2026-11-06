// The sidebar on a phone (the user, 2026-11-06): a sheet from the window's foot, half its height,
// taking the composer's place: one of the two is at the bottom at a time, the composer sinking as
// the sheet rises and back. Nothing dims: the chat above is seen and scrolls as ever, and a tap on
// it puts the sheet away, as picking a session does. The sidebar's key (drawer.tsx SideKey) opens
// and shuts it; a swipe up from the composer brings it too, following the finger, and one down on
// its grip or head puts it away; Esc does too. While searching it stands taller, for the keyboard.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { t } from "./i18n";
import { cn } from "./ui";

/** Where a swipe up brings the sheet: the chat's bottom stack (chat.tsx), the composer in it. */
export const PULL = "data-sheet-pull";
const HEAD = 64; // px from the sheet's top where a swipe down may begin: the grip and the head

export function Sheet({ open, setOpen, tall, onHeight, children }: { open: boolean; setOpen: (o: boolean) => void; tall: boolean; onHeight: (h: number) => void; children: ReactNode }) {
  const panel = useRef<HTMLDivElement>(null);
  // While a finger drags it: how far it is out, 0..1; null when it is not dragged.
  const [drag, setDrag] = useState<number | null>(null);
  const now = useRef({ open, setOpen });
  now.current = { open, setOpen };

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
  // down from the grip or the head while out. Undecided till it has moved 8px; then it is the
  // sheet's only if it went more down or up than across, and the right way.
  useEffect(() => {
    let from: { x: number; y: number; open: boolean } | null = null;
    let mine = false;
    let at = 0;
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
      } else if (!el.closest(`[${PULL}]`) || el.closest("textarea, [role=listbox], [role=dialog], [role=menu]")) return;
      from = { x: p.clientX, y: p.clientY, open };
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
        if (Math.abs(dy) < dx * 1.2 || (from.open ? dy < 0 : dy > 0)) return void (from = null);
        mine = true;
      }
      e.preventDefault();
      const h = height();
      at = Math.min(1, Math.max(0, (from.open ? h - dy : -dy) / h));
      last = { y: p.clientY, t: e.timeStamp, v: (p.clientY - last.y) / Math.max(1, e.timeStamp - last.t) };
      setDrag(at);
    };
    const end = () => {
      if (from && mine) {
        // A flick decides by its way; a slow drag by where it was let go.
        const out = Math.abs(last.v) > 0.4 ? last.v < 0 : at > 0.5;
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
      aria-label={t("nav.sessions")}
      inert={!open && drag === null}
      className={cn(
        "g-sheet vt-drawer fixed inset-x-0 bottom-0 z-40 flex flex-col overflow-hidden rounded-t-2xl bg-paper pb-[env(safe-area-inset-bottom)] shadow-[var(--shadow-float)]",
        tall ? "h-[85dvh]" : "h-[50dvh] min-h-[320px]",
        drag !== null && "[transition:none]",
        out === 0 && "shadow-none",
      )}
      // Shut, past its own height and its shadow too.
      style={{ transform: `translateY(calc(${(1 - out) * 100}% + ${(1 - out) * 20}px))` }}
    >
      <div aria-hidden className="grid h-4 shrink-0 place-items-end justify-center">
        <span className="h-1 w-9 rounded-full bg-fill-2" />
      </div>
      <div className="relative min-h-0 flex-1">{children}</div>
    </div>
  );
}
