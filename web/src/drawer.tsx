// The sidebar as a drawer (the user, 2026-11-06): on a phone or a portrait screen always, on a
// computer once the sidebar is made small. The chat is the one main pane; the drawer, a whole
// rounded card a little off the window's edges, slides out over it from the left, two thirds of a
// phone's width; a key at the bottom left brings it. On a phone it is modal: the chat dims under
// it, a swipe from the left edge brings it following the finger, a swipe back or a tap beside it
// puts it away. On a computer it lives beside the chat (the user, 2026-11-06): nothing dims, the
// chat is used as ever, and it stays till put away (its key, ⌘\, Esc).
import { PanelLeft } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { t } from "./i18n";
import { cn, IconKey } from "./ui";

const EDGE = 24; // px from the left edge where a swipe may begin

export function Drawer({ open, setOpen, phone, modal, children }: { open: boolean; setOpen: (o: boolean) => void; phone: boolean; modal: boolean; children: ReactNode }) {
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
    if (!modal) return;
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
  }, [modal]);

  const out = drag ?? (open ? 1 : 0);
  return (
    <>
      {modal && <div
        aria-hidden
        onClick={() => setOpen(false)}
        className={cn("fixed inset-0 z-40 bg-[oklch(0_0_0/0.22)] dark:bg-[oklch(0_0_0/0.45)]", drag === null && "transition-opacity duration-(--dur-3) ease-(--ease-out)", out === 0 && "pointer-events-none")}
        style={{ opacity: out }}
      />}
      <div
        ref={panel}
        role="dialog"
        aria-modal={modal && open}
        aria-label={t("nav.sessions")}
        inert={!open && drag === null}
        className={cn(
          "g-drawer fixed top-[max(8px,env(safe-area-inset-top))] bottom-[max(8px,env(safe-area-inset-bottom))] left-2 z-40 flex overflow-hidden rounded-xl bg-paper shadow-[var(--shadow-float)]",
          phone && "w-[min(max(66.667vw,264px),calc(100vw-48px))]",
          drag !== null && "[transition:none]",
          out === 0 && "shadow-none",
        )}
        // Shut, past its own width and the gap to the edge, its shadow too.
        style={{ transform: `translateX(calc(${(out - 1) * 100}% - ${(1 - out) * 24}px))` }}
      >
        {children}
      </div>
    </>
  );
}

/** What brings the drawer, at the bottom left, over the page, not in its layout (the user,
 *  2026-11-06): on a wide window a frosted round key in the margin left of the composer, level with
 *  its foot; narrower, where the composer is as wide as the page, a slim handle on the left edge just
 *  above it, in the conversation's gutter. above: the composer's stack holds it (the handle over its
 *  top); else the page's foot. */
export function DrawerKey({ onClick, above }: { onClick: () => void; above?: boolean }) {
  const label = `${t("nav.sidebar")} · ${t("nav.sidebarKey")}`;
  return (
    <>
      <span className="absolute bottom-[max(14px,calc(env(safe-area-inset-bottom)+6px))] left-3 z-10 hidden min-[1000px]:block">
        <IconKey label={label} tipSide="right" onClick={onClick} className="glass thick float size-10 rounded-full text-ink-2 hover:bg-fill hover:text-ink">
          <PanelLeft size={18} />
        </IconKey>
      </span>
      <button
        type="button"
        aria-label={label}
        onClick={onClick}
        className={cn("g-press group absolute left-0 z-10 grid h-16 w-6 place-items-center min-[1000px]:hidden", above ? "bottom-full mb-2" : "bottom-[max(24px,calc(env(safe-area-inset-bottom)+16px))]")}
      >
        <span aria-hidden className="h-10 w-1 rounded-r-full bg-ink-4 opacity-70 transition-[opacity,width] duration-(--dur-1) group-hover:w-1.5 group-hover:opacity-100 group-active:w-1.5" />
      </button>
    </>
  );
}
