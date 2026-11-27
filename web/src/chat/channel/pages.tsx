// A menu's next level (kumo, 2026-11-25): inside a Paged menu (every menu at the bar's right) in
// the phone's layout (PHONE: a window tall and narrow, a computer's too; never by the device), the
// menu's next page, its row pressed, 返回 at its head: no menu beside it. Elsewhere, a submenu out
// of its row.
//
// The pages are pushed (kumo, 2026-11-25: one gone and the next faded in flashed): the next comes
// in whole from the right as the one under it goes out to the left, both seen as they move; back,
// the other way. Each page is a layer (g-page): the one shown is in the flow, the panel as tall as
// it (Tall glides); the others lie over it, out of the flow, out of sight and not to be pressed.
import { Menu } from "@base-ui/react/menu";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { createContext, useContext, useEffect, useId, useState, type CSSProperties, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Tall } from "../../create";
import { motion } from "../../motion";
import { cn, MenuPortal, PHONE, useMedia } from "../../ui";
import { ITEM } from "./invite";

type Page = { id: string; title: string };
export type Pages = { stack: Page[]; push: (p: Page) => void; pop: () => void; slot: HTMLElement | null; gone: Page | null };
const Ctx = createContext<Pages | null>(null);

/** Whether a menu's next levels are its pages: the phone's layout, whatever the device (kumo,
 *  2026-11-25: a computer's window as tall and narrow is it, an unfolded phone is not). */
export const usePaged = () => useMedia(PHONE);

/** The pages of the menu it is in, if they are pages (Paged, the phone's layout); else null. */
export const usePages = () => useContext(Ctx);

/** Where a layer is: shown (top), under the one shown (under), or going away to the right (gone). */
type Place = "top" | "under" | "gone";

/** One page of a Paged menu. first: the list itself, there as the menu opens (it does not come in). */
function Layer({ place, first, children }: { place: Place; first?: boolean; children: ReactNode }) {
  return (
    <div data-page={place} inert={place !== "top" || undefined} aria-hidden={place !== "top" || undefined} className={cn("g-page flex flex-col", first && "g-page-first", place !== "top" && "absolute inset-x-0 top-0")}>
      {children}
    </div>
  );
}

/** A menu whose next levels are its pages in the phone's layout (or everywhere when force is set,
 *  so a submenu switches pages in place instead of opening a third-level popup). The list and
 *  every page under the one shown stay drawn: what they were left at is kept. */
export function Paged({ force, children }: { force?: boolean; children: ReactNode }) {
  const paged = usePaged() || !!force;
  const [stack, setStack] = useState<Page[]>([]);
  // The page gone back from, drawn till it is out.
  const [gone, setGone] = useState<Page | null>(null);
  const [slot, setSlot] = useState<HTMLDivElement | null>(null);
  useEffect(() => {
    if (!gone) return;
    const t = setTimeout(() => setGone(null), motion.dur(3) + 40);
    return () => clearTimeout(t);
  }, [gone]);
  if (!paged) return <>{children}</>;
  const push = (p: Page) => (setGone(null), setStack((s) => [...s, p]));
  const pop = () => (setGone(stack.at(-1) ?? null), setStack((s) => s.slice(0, -1)));
  return (
    <Ctx.Provider value={{ stack, push, pop, slot, gone }}>
      <Tall>
        <div className="relative overflow-hidden">
          <Layer place={stack.length ? "under" : "top"} first>
            {children}
          </Layer>
          <div ref={setSlot} />
        </div>
      </Tall>
    </Ctx.Provider>
  );
}

/** The page id of a Paged menu, drawn while it is in the stack (or going back out): ‹ title at its
 *  head, back (bare: none). Its key is the caller's: a row (Deeper), or a key of its own (the members' +). */
export function PageOf({ pages, id, title, bare, children }: { pages: Pages; id: string; title: string; bare?: boolean; children: ReactNode }) {
  const at = pages.stack.findIndex((p) => p.id === id);
  // Not in the stack: no page drawn (with none open, at -1 is no top: every row's page was drawn
  // at once, kumo 2026-11-25).
  const place: Place | null = at < 0 ? (pages.gone?.id === id ? "gone" : null) : at === pages.stack.length - 1 ? "top" : "under";
  if (!place || !pages.slot) return null;
  return createPortal(
    <Layer place={place}>
      {/* bare: no 返回 at its head, a page with no way back (邀请 from the members' +, kumo 2026-11-26). */}
      {!bare && (
        <Menu.Item closeOnClick={false} onClick={pages.pop} className={cn(ITEM, "text-ink-2")}>
          <ChevronLeft size={15} />
          <span className="min-w-0 flex-1 truncate">{title}</span>
        </Menu.Item>
      )}
      {children}
    </Layer>,
    pages.slot,
  );
}

/** A row opening a next level: row, what the row shows (its icon, name, what is picked); title,
 *  the page's head; popup, the submenu's surface; bare: no › at its end. */
export function Deeper({ row, title, i, popup, bare, disabled, children }: { row: ReactNode; title: string; i: number; popup: string; bare?: boolean; disabled?: boolean; children: ReactNode }) {
  const pages = useContext(Ctx);
  const id = useId();
  const style = { "--i": i } as CSSProperties;
  if (pages) {
    return (
      <>
        <Menu.Item closeOnClick={false} disabled={disabled} onClick={() => pages.push({ id, title })} style={style} className={cn(ITEM, "data-disabled:opacity-40")}>
          {row}
        </Menu.Item>
        <PageOf pages={pages} id={id} title={title}>
          {children}
        </PageOf>
      </>
    );
  }
  return (
    <Menu.SubmenuRoot>
      <Menu.SubmenuTrigger disabled={disabled} style={style} className={cn(ITEM, "data-disabled:opacity-40")}>
        {row}
        {!bare && <ChevronRight size={14} className="text-ink-4" />}
      </Menu.SubmenuTrigger>
      <MenuPortal>
        <Menu.Positioner className="z-50" sideOffset={4} collisionPadding={8}>
          <Menu.Popup className={popup}>{children}</Menu.Popup>
        </Menu.Positioner>
      </MenuPortal>
    </Menu.SubmenuRoot>
  );
}
