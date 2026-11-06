// The sidebar as a drawer (the user, 2026-11-06): on a portrait touch screen always, on a computer
// once the sidebar is made small; on a phone it is a sheet from the foot (sheet.tsx). A whole
// rounded card at the window's top left, two thirds of its height, sliding out from the left over
// the chat: nothing dims, nothing makes room, the chat is used as ever under and beside it. One key
// opens and shuts it and makes the docked sidebar small, at one place on the screen whatever the
// state (SideKey, the top left); Esc puts it away too.
import { Minimize2, PanelLeft } from "lucide-react";
import { useEffect, type ReactNode } from "react";
import { t } from "./i18n";
import { cn, IconKey } from "./ui";

export function Drawer({ open, setOpen, children }: { open: boolean; setOpen: (o: boolean) => void; children: ReactNode }) {
  useEffect(() => {
    if (!open) return;
    const key = (e: KeyboardEvent) => e.key === "Escape" && !e.defaultPrevented && !document.querySelector("[role=dialog][data-open], [role=menu][data-open]") && setOpen(false);
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [open]);

  return (
    <div
      role="region"
      aria-label={t("nav.sessions")}
      inert={!open}
      className={cn(
        "g-drawer vt-drawer fixed top-1 left-1 z-40 flex h-[66.667dvh] min-h-[360px] overflow-hidden rounded-xl bg-paper shadow-[var(--shadow-float)]",
        !open && "shadow-none",
      )}
      // Shut, past its own width and the gap to the edge, its shadow too.
      style={{ transform: open ? "none" : "translateX(calc(-100% - 20px))" }}
    >
      {children}
    </div>
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
