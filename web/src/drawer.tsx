// The sidebar as a drawer (the user, 2026-11-06): on a phone or a portrait screen always, on a
// computer once the sidebar is made small. A whole rounded card at the window's top left, two
// thirds of its height, sliding out from the left over the chat: nothing dims, nothing makes room,
// the chat is used as ever under and beside it. One key opens and shuts it and makes the docked
// sidebar small, at one place on the screen whatever the state (SideKey, the top left). On a touch
// screen a swipe from the left edge brings it too, following the finger, and a swipe back puts it
// away; Esc does too.
import { Minimize2, PanelLeft } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { t } from "./i18n";
import { cn, IconKey } from "./ui";

const EDGE = 24; // px from the left edge where a swipe may begin

export function Drawer({ open, setOpen, phone, children }: { open: boolean; setOpen: (o: boolean) => void; phone: boolean; children: ReactNode }) {
  const panel = useRef<HTMLDivElement>(null);
  // While a finger drags it: how far it is out, 0..1; null when it is not dragged.
  const [drag, setDrag] = useState<number | null>(null);
  const now = useRef({ open, setOpen });
  now.current = { open, setOpen };

  useEffect(() => {
    if (!open) return;
    const key = (e: KeyboardEvent) => e.key === "Escape" && !e.defaultPrevented && !document.querySelector("[role=dialog][data-open], [role=menu][data-open]") && setOpen(false);
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [open]);

  // A horizontal swipe: from the left edge while shut, anywhere while open. Undecided till it has
  // moved 8px; then it is the drawer's only if it went more across than down.
  useEffect(() => {
    if (!phone) return;
    let from: { x: number; y: number; t: number; open: boolean } | null = null;
    let mine = false;
    let at = 0;
    let last = { x: 0, t: 0, v: 0 };
    const width = () => panel.current?.offsetWidth || innerWidth * 0.67;
    const start = (e: TouchEvent) => {
      const p = e.touches[0];
      if (e.touches.length !== 1 || !p) return;
      const { open } = now.current;
      if (!open && p.clientX > EDGE) return;
      from = { x: p.clientX, y: p.clientY, t: e.timeStamp, open };
      mine = false;
      last = { x: p.clientX, t: e.timeStamp, v: 0 };
    };
    const move = (e: TouchEvent) => {
      const p = e.touches[0];
      if (!from || !p) return;
      const dx = p.clientX - from.x;
      const dy = p.clientY - from.y;
      if (!mine) {
        if (Math.hypot(dx, dy) < 8) return;
        if (Math.abs(dx) < Math.abs(dy) * 1.2 || (from.open ? dx > 0 : dx < 0)) return void (from = null);
        mine = true;
      }
      e.preventDefault();
      const w = width();
      at = Math.min(1, Math.max(0, (from.open ? w + dx : dx) / w));
      last = { x: p.clientX, t: e.timeStamp, v: (p.clientX - last.x) / Math.max(1, e.timeStamp - last.t) };
      setDrag(at);
    };
    const end = () => {
      if (from && mine) {
        // A flick decides by its way; a slow drag by where it was let go.
        const out = Math.abs(last.v) > 0.4 ? last.v > 0 : at > 0.5;
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
  }, [phone]);

  const out = drag ?? (open ? 1 : 0);
  return (
    <>
      <div
        ref={panel}
        role="region"
        aria-label={t("nav.sessions")}
        inert={!open && drag === null}
        className={cn(
          "g-drawer vt-drawer fixed top-1 left-1 z-40 flex h-[66.667dvh] min-h-[360px] overflow-hidden rounded-xl bg-paper shadow-[var(--shadow-float)]",
          phone && "w-[min(340px,calc(100vw-48px))]",
          drag !== null && "[transition:none]",
          out === 0 && "shadow-none",
        )}
        // Shut, past its own width and the gap to the edge, its shadow too.
        style={{ transform: `translateX(calc(${(out - 1) * 100}% - ${(1 - out) * 20}px))` }}
      >
        {children}
      </div>
    </>
  );
}

/** The sidebar's one key, fixed at the window's top left, over the docked sidebar's head, the
 *  card's, or the chat's bar, at the same place in each (the user, 2026-11-06): docked, it makes
 *  the sidebar small; small, it brings the card out and puts it away. Those heads keep its place
 *  free (KEY_SLOT). */
export function SideKey({ state, onClick }: { state: "docked" | "out" | "in"; onClick: () => void }) {
  const label = state === "docked" ? `${t("nav.hideSidebar")} · ${t("nav.sidebarKey")}` : state === "out" ? t("nav.closeSidebar") : t("nav.sidebar");
  return (
    <span className="vt-side-key fixed top-2 left-2 z-45 pointer-coarse:top-1.5">
      <IconKey label={label} tipSide="right" onClick={onClick} aria-expanded={state !== "in"} className={cn(state === "out" && "text-ink")}>
        {state === "docked" ? <Minimize2 size={16} /> : <PanelLeft size={17} />}
      </IconKey>
    </span>
  );
}

/** The width the heads under SideKey keep free at their left. */
export const KEY_SLOT = "pl-11 pointer-coarse:pl-13";
