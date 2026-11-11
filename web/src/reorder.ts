// The list's order by hand (the user, 2026-11-09): a group's row dragged among all groups' rows, a
// folder among those in its group, a session among those beside it (in its group's root or its
// folder). A session hung under its launcher (tree.ts standing) is not ordered: dragged anywhere in
// the list out from under that launcher, it is taken out (the user, 2026-11-10). Pinned ones are ordered apart from the rest; one dropped on a pinned row is
// pinned, on another unpinned. A folder pinned is above the sessions at its group's root; one
// dropped on any of those is pinned, last of the pinned. A session or folder dragged keeps the key
// it was dropped at (Session.order, the carried "places") in place of its last activity: it
// stays where it was put while those never dragged are still sorted by when they last worked.
// With a mouse only, as a finger's long press is the menu: on a phone a group's menu moves it.
import { useSyncExternalStore } from "react";
import type { Places, Session } from "./api/types.gen";
import { useCarried } from "./store";
import { folderKey, type Folder } from "./tree";

/** What is dragged: a group (its ID), a folder, a session, or a session hung under its launcher
 *  ("h", lane: that launcher's ID), and the rows it may be dropped among. */
export type Dragged = { kind: "ws" | "f" | "s" | "h"; id: string; lane: string };
/** The row's edge it goes to: above it or under it. */
export type Edge = "top" | "bottom";

/** The row a drop would go to now (its mark, dropMark), the row the line is drawn at (that one,
 *  or the one the drop lands above) and its edge. */
export type Over = { at: string; line: string; edge: Edge };
/** A drop a row takes of another kind, and the row whose top it lands at (dropMark). */
export type Also = { kind: Dragged["kind"]; lane: string; drop: (d: Dragged) => void; line: string };

let dragged: Dragged | null = null;
let over: Over | null = null;
const subs = new Set<() => void>();
const subscribe = (f: () => void) => (subs.add(f), () => void subs.delete(f));
const emit = () => subs.forEach((f) => f());
const setOver = (o: Over | null) => {
  if (o?.at === over?.at && o?.line === over?.line && o?.edge === over?.edge) return;
  over = o;
  emit();
};

export const startDrag = (d: Dragged) => ((dragged = d), emit());
export const endDrag = () => ((dragged = null), (over = null), emit());

/** Whether a drag is over the rows of its launcher (data-under, sidebar.tsx): a hung one is not
 *  taken out there. */
const home = (e: React.DragEvent<HTMLElement>, d: Dragged) => e.target instanceof Element && e.target.closest("[data-under]")?.getAttribute("data-under") === d.lane;

/** What the list takes, round its rows: over no row a drop goes to (none took the dragover) or
 *  out of the list, no line. Not on leaving a row: the line would blink between two. A hung one
 *  dropped anywhere but under its launcher: out (detach). */
export const listDrop = (detach: (id: string) => void) => ({
  onDragOver: (e: React.DragEvent<HTMLElement>) => {
    if (!e.defaultPrevented) setOver(null);
    if (dragged?.kind !== "h" || home(e, dragged)) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
  },
  onDragLeave: (e: React.DragEvent<HTMLElement>) => void (!e.currentTarget.contains(e.relatedTarget as Node | null) && setOver(null)),
  onDrop: (e: React.DragEvent<HTMLElement>) => {
    const d = dragged;
    if (d?.kind !== "h" || home(e, d)) return;
    e.preventDefault();
    endDrag();
    detach(d.id);
  },
});

/** The row a drop would go to now (the list draws one line, gliding from row to row). */
export const useOver = () => useSyncExternalStore(subscribe, () => over);
/** That the row of id is the one dragged now: it fades while it is. */
export const useDragged = (kind: Dragged["kind"], id: string) => useSyncExternalStore(subscribe, () => dragged?.kind === kind && dragged.id === id);
/** The row's mark the list finds it by, to draw the line at: data-place (data-drop is a note's, notes.tsx). */
export const dropMark = (d: Dragged) => `${d.kind}:${d.lane}:${d.id}`;

/** What a row takes to be dropped on: by one of its kind and lane, not itself; also, by one of
 *  another kind (a folder on a session at its group's root). */
export function dropTarget(kind: Dragged["kind"], id: string, lane: string, drop: (d: Dragged, edge: Edge) => void, also?: Also) {
  const at = dropMark({ kind, id, lane });
  const pick = () => {
    const d = dragged;
    if (d?.kind === kind && d.lane === lane && d.id !== id) return "own";
    if (also && d?.kind === also.kind && d.lane === also.lane) return "also";
    return null;
  };
  return {
    "data-place": at,
    onDragOver: (e: React.DragEvent<HTMLElement>) => {
      const how = pick();
      if (!how) return;
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      const r = e.currentTarget.getBoundingClientRect();
      setOver(how === "also" ? { at, line: also!.line, edge: "top" } : { at, line: at, edge: e.clientY < r.top + r.height / 2 ? "top" : "bottom" });
    },
    onDrop: (e: React.DragEvent<HTMLElement>) => {
      const how = pick();
      if (!how || over?.at !== at) return;
      e.preventDefault();
      const d = dragged!;
      const edge = over.edge;
      endDrag();
      if (how === "also") also!.drop(d);
      else drop(d, edge);
    },
  };
}

/** What a session is sorted by: where it was dragged to, else its last activity (as the server's
 *  session.List). */
export const sortKey = (s: Session) => s.order || s.updatedAt;

/** The list sorted as the server sorts it: pinned first, then by sortKey, the latest first. */
export const sortList = (list: Session[]) => [...list].sort((a, b) => Number(!!b.pinned) - Number(!!a.pinned) || sortKey(b) - sortKey(a));

/** The key one goes to, dropped at edge of the row at j in zone (those it goes among, as listed,
 *  itself left out; key: what each is sorted by, the latest first): between its neighbours there,
 *  now at the top, a second under the last at the foot. */
function keyAt<T>(zone: T[], j: number, key: (x: T) => number, now: number) {
  const prev = zone[j - 1];
  const next = zone[j];
  return prev && next ? (key(prev) + key(next)) / 2 : next ? Math.max(now, key(next) + 1) : key(prev!) - 1000;
}

/** Where session s goes, dropped at edge of target among sibs (the sessions beside it, as listed):
 *  the key it takes, and pinned as the target is. */
export function placeSession(sibs: Session[], s: Session, target: Session, edge: Edge, now = Date.now()): { order: number; pinned: boolean } {
  const pinned = !!target.pinned;
  const zone = sibs.filter((x) => !!x.pinned === pinned && x.id !== s.id);
  const j = zone.findIndex((x) => x.id === target.id) + (edge === "bottom" ? 1 : 0);
  return { order: keyAt(zone, j, sortKey, now), pinned };
}

/** Where folder f goes, dropped at edge of target among sibs (the folders of its group, as
 *  listed): the key it takes, and pinned as the target is. */
export function placeFolder(sibs: Folder[], f: Folder, target: Folder, edge: Edge, places: Places, now = Date.now()): { order: number; pinned: boolean } {
  const pinned = !!target.pinned;
  const zone = sibs.filter((x) => !!x.pinned === pinned && x.path !== f.path);
  const j = zone.findIndex((x) => x.path === target.path) + (edge === "bottom" ? 1 : 0);
  return { order: keyAt(zone, j, (x) => folderKey(x, places.order), now), pinned };
}

/** Folder f pinned last of its group's pinned (dropped on a session at its root): the key under
 *  the last, none if it is the first. */
export function pinFolder(sibs: Folder[], f: Folder, places: Places): { order?: number; pinned: true } {
  const last = sibs.filter((x) => x.pinned && x.path !== f.path).at(-1);
  return last ? { order: folderKey(last, places.order) - 1000, pinned: true } : { pinned: true };
}

/** places with the folder of path put: its key (0 none) and pinned or not, as a field given. */
export const withPlace = (places: Places, path: string, p: { order?: number; pinned?: boolean }): Places => {
  const order = { ...places.order };
  if (p.order !== undefined) {
    if (p.order) order[path] = p.order;
    else delete order[path];
  }
  const pinned = p.pinned === undefined ? places.pinned : p.pinned ? [...places.pinned.filter((x) => x !== path), path] : places.pinned.filter((x) => x !== path);
  return { ...places, order, pinned };
};

export const NO_PLACES: Places = { groups: [], order: {}, pinned: [] };
/** The carried "places": where the rows were put by hand, the groups' and the folders'. */
export const usePlaces = () => useCarried<Places>("places") ?? NO_PLACES;

/** The groups' rows' keys with key moved to edge of target: the order PUT /v1/internal/groups/order takes.
 *  stored: the order there was; shown: the rows as listed now (those not stored after the
 *  stored). A row stored but not listed now (none of its sessions shown) keeps its place. */
export function placeGroup(stored: string[], shown: string[], key: string, target: string, edge: Edge): string[] {
  const all = [...stored, ...shown.filter((k) => !stored.includes(k))].filter((k) => k !== key);
  all.splice(all.indexOf(target) + (edge === "bottom" ? 1 : 0), 0, key);
  return all;
}
