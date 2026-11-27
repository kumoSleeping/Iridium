// Favorites across the Iridiums in the desktop app (internal/desktop/favs.go; otae-c, kumo, 霓虹,
// 2026-11-26): this page tells the app its own favorites, a few words a row (tellFavs), and the
// app gives it every Iridium's, in one order (useCross): the list draws them among its own, each
// of another Iridium with that Iridium's face after its name. One pressed: the app shows its chat
// beside this page's list (assemble: this page then draws its list alone, part "side"; the other's
// its chat alone, part "chat"). In a browser none of this is there: the list is as it was.
import { useSyncExternalStore } from "react";
import type { Color, Status } from "./api/types.gen";
import { ask } from "./irapp";
import { put, setFavorite } from "./favorites";
import { onMove } from "./route";

/** A favorite as its own page tells it: what its row shows. */
export type FavItem = {
  id: string;
  title: string;
  untitled?: boolean;
  agent: string;
  status: Status;
  unread: number;
  quiet?: boolean;
  at?: "me" | "all";
  line: string;
  updatedAt: number;
  color?: Color;
  /** The channel's or agent's own face (small data: URL), so another Iridium's list shows it
   *  without cross-origin cookies. */
  face?: string;
};
export type FavServer = { title: string; face?: string; local?: boolean };
export type Cross = { self: string; order: string[]; items: Record<string, FavItem[]>; servers: Record<string, FavServer> };
/** What this page is while assembled: its list alone (side), its chat alone (chat), or whole (""). */
export type Part = { part: "" | "side" | "chat"; title?: string; face?: string };

let cross: Cross | null = null;
let part: Part = { part: "" };
const subs = new Set<() => void>();
const emit = () => subs.forEach((f) => f());
const sub = (f: () => void) => (subs.add(f), () => void subs.delete(f));

window.addEventListener("ir-app", (e) => {
  const d = (e as CustomEvent).detail as { favs?: Cross; favput?: string[]; favdrop?: string; assemble?: Part; sidebarW?: number };
  if (d.favs) (cross = { ...d.favs, order: d.favs.order ?? [], items: d.favs.items ?? {}, servers: d.favs.servers ?? {} }), emit();
  if (Array.isArray(d.favput)) put(d.favput);
  if (typeof d.favdrop === "string") setFavorite(d.favdrop, false);
  // The line between the app's two pages dragged: the list's width (sidebar.tsx keeps it).
  if (typeof d.sidebarW === "number") window.dispatchEvent(new CustomEvent("ir-side-width", { detail: d.sidebarW }));
  if (d.assemble) {
    part = d.assemble;
    if (part.part) document.documentElement.dataset.irPart = part.part;
    else delete document.documentElement.dataset.irPart;
    emit();
  }
});

/** Every Iridium's favorites, as the app gives them; null: not in the app (or not told yet). */
export const useCross = () => useSyncExternalStore(sub, () => cross, () => null);
/** What this page is while assembled. */
export const usePart = () => useSyncExternalStore(sub, () => part, () => part);

/** A favorite's key in the order: its Iridium, #, its id. */
export const favKey = (server: string, id: string) => `${server}#${id}`;
export const keyServer = (key: string) => key.slice(0, key.lastIndexOf("#"));
export const keyId = (key: string) => key.slice(key.lastIndexOf("#") + 1);

let said = "";
let next: FavItem[] | null = null;
let timer: ReturnType<typeof setTimeout> | undefined;
/** This page's favorites, as its list shows them, told to the app (gathered a moment, only as they
 *  change). */
export function tellFavs(items: FavItem[]) {
  next = items;
  timer ??= setTimeout(() => {
    timer = undefined;
    const now = JSON.stringify({ items: next });
    if (now === said) return;
    said = now;
    ask("favs", now);
  }, 400);
}

/** Another Iridium's favorite: shown beside this page (open), in its own window (away), taken off. */
export const crossAsk = (op: "open" | "away" | "drop", server: string, id: string) => ask("fav", JSON.stringify({ op, server, id }));
/** The favorites put in this order (keys), every Iridium's. */
export const crossOrder = (keys: string[]) => ask("favorder", JSON.stringify(keys));
/** The list's width, for the app's two pages side by side. */
export const tellSide = (w: number) => ask("sidebar", String(Math.round(w)));
/** The right one's name pressed: that Iridium whole. */
export const assembledFull = () => ask("assemble", "full");

// The left one's list chose one of its own chats: this page whole again.
onMove(() => {
  if (part.part === "side") ask("assemble", "off");
});
